package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "ngfaas/pkg/api"
)

// WorkerState simule l'Hyperviseur et garde trace des MicroVMs/Sandboxes allumées
type WorkerState struct {
	sync.RWMutex
	activeSandboxes map[string]string // map[function_name]virtual_nic_ip
}

// Fonction utilitaire pour simuler l'allumage d'une MicroVM (Cold Start)
func (ws *WorkerState) bootSandbox(funcName string) string {
	ws.Lock()
	defer ws.Unlock()

	// Si elle a été démarrée par un autre thread entre temps (Warm Start)
	if nic, exists := ws.activeSandboxes[funcName]; exists {
		return nic
	}

	fmt.Printf("❄️  [Cold Start] Démarrage de l'Hyperviseur pour la fonction '%s'...\n", funcName)
	time.Sleep(200 * time.Millisecond) // Simule le temps de démarrage (VM + OS)

	// Génère une fausse IP (NIC virtuel) pour cette Sandbox
	virtualNIC := fmt.Sprintf("10.0.0.%d", len(ws.activeSandboxes)+2)
	ws.activeSandboxes[funcName] = virtualNIC

	fmt.Printf("✅ [Cold Start Terminé] Sandbox '%s' prête sur le NIC virtuel %s\n", funcName, virtualNIC)
	return virtualNIC
}

func main() {
	workerID := "worker-" + uuid.New().String()[:8]
	workerPort := 9090 // Port d'écoute pour ce Worker (l'Agent FaaS)

	ws := &WorkerState{
		activeSandboxes: make(map[string]string),
	}

	fmt.Printf("⚙️ Démarrage de l'Agent Worker %s...\n", workerID)

	// 1. Connexion Redis (pour mettre à jour le statut des jobs asynchrones)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "", DB: 0})

	// 2. Connexion NATS (pour écouter la file d'attente Asynchrone)
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("❌ Impossible de se connecter à NATS : %v", err)
	}
	defer nc.Close()

	// 3. Inscription auprès du Controller (gRPC)
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("❌ Impossible de se connecter au Controller : %v", err)
	}
	defer conn.Close()
	client := pb.NewControllerServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = client.RegisterWorker(ctx, &pb.RegisterWorkerRequest{
		WorkerId:  workerID,
		IpAddress: "127.0.0.1",
		Port:      int32(workerPort),
	})
	if err != nil {
		log.Fatalf("❌ Erreur d'enregistrement Worker: %v", err)
	}
	fmt.Println("✅ Enregistré auprès du Control Plane.")

	// 4. Écoute de la file d'attente Asynchrone (NATS)
	nc.QueueSubscribe("async.jobs", "worker_queue", func(m *nats.Msg) {
		var job map[string]string
		json.Unmarshal(m.Data, &job)
		funcName := job["function"]
		jobID := job["job_id"]

		fmt.Printf("📥 [Async Job] Prise en charge de la tâche %s\n", jobID)

		// Simule l'exécution
		nic := ws.bootSandbox(funcName)
		fmt.Printf("🔥 Exécution asynchrone sur NIC %s...\n", nic)
		time.Sleep(500 * time.Millisecond) // Temps de calcul

		// Met le résultat dans Redis
		rdb.Set(context.Background(), "job:"+jobID, "SUCCESS (Résultat de la fonction)", 24*time.Hour)
		fmt.Printf("✅ [Async Job] Tâche %s terminée.\n", jobID)
	})

	// -------------------------------------------------------------
	// 5. SERVEUR HTTP (L'Agent FaaS écoutant les requêtes Synchrones du DataPlane)
	// -------------------------------------------------------------
	http.HandleFunc("/execute/", func(w http.ResponseWriter, r *http.Request) {
		funcName := r.URL.Path[len("/execute/"):]

		ws.RLock()
		nic, exists := ws.activeSandboxes[funcName]
		ws.RUnlock()

		if !exists {
			// Le Worker Agent demande à l'Hyperviseur de créer la sandbox
			nic = ws.bootSandbox(funcName)
		} else {
			// Le Worker Agent transfère directement la requête au NIC existant
			fmt.Printf("🔥 [Warm Start] Transfert direct de '%s' au NIC virtuel %s\n", funcName, nic)
		}

		// Simulation de l'exécution dans la VM
		time.Sleep(50 * time.Millisecond)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status": "success", "executed_on_worker": "%s", "nic": "%s"}`, workerID, nic)
	})

	fmt.Printf("🌐 Worker Agent %s en écoute sur le port %d\n", workerID, workerPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", workerPort), nil); err != nil {
		log.Printf("Erreur du serveur HTTP: %v", err)
	}
}
