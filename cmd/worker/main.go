package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
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

type WorkerState struct {
	sync.RWMutex
	activeSandboxes map[string]string // map[function_name]virtual_nic_ip
	currentLoad     int               // Nombre de requetes en cours
}

func (ws *WorkerState) bootSandbox(funcName string) string {
	ws.Lock()
	defer ws.Unlock()

	if nic, exists := ws.activeSandboxes[funcName]; exists {
		return nic
	}

	fmt.Printf("❄️  [Cold Start] Demarrage Hyperviseur pour '%s'...\n", funcName)
	time.Sleep(200 * time.Millisecond) // Simule creation de MicroVM

	virtualNIC := fmt.Sprintf("10.0.0.%d", len(ws.activeSandboxes)+2)
	ws.activeSandboxes[funcName] = virtualNIC
	return virtualNIC
}

// HeartbeatMsg format detaille pour l'intelligence de routage
type HeartbeatMsg struct {
	WorkerID  string            `json:"worker_id"`
	Address   string            `json:"address"`
	Load      int               `json:"load"`
	Functions map[string]string `json:"functions"`
}

// Fonction pour obtenir un port libre dynamiquement
func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func main() {
	workerID := "worker-" + uuid.New().String()[:8]

	port, err := getFreePort()
	if err != nil {
		log.Fatalf("Impossible de trouver un port libre : %v", err)
	}
	workerAddress := fmt.Sprintf("127.0.0.1:%d", port)

	ws := &WorkerState{
		activeSandboxes: make(map[string]string),
	}

	fmt.Printf("⚙️ Agent %s en ecoute sur %s\n", workerID, workerAddress)

	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "", DB: 0})

	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("❌ NATS injoignable : %v", err)
	}
	defer nc.Close()

	// Enregistrement aupres du Controller (gRPC)
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("❌ Controller injoignable : %v", err)
	}
	defer conn.Close()
	client := pb.NewControllerServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = client.RegisterWorker(ctx, &pb.RegisterWorkerRequest{
		WorkerId:  workerID,
		IpAddress: "127.0.0.1",
		Port:      int32(port),
	})
	if err != nil {
		log.Fatalf("❌ Erreur enregistrement Worker: %v", err)
	}

	// -------------------------------------------------------------
	// TELEMETRIE : Envoi periodique de l'etat detaille
	// -------------------------------------------------------------
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			ws.RLock()

			// Copie profonde de la map pour eviter une erreur de concurrence pendant la serialisation JSON
			funcsCopy := make(map[string]string)
			for k, v := range ws.activeSandboxes {
				funcsCopy[k] = v
			}

			hb := HeartbeatMsg{
				WorkerID:  workerID,
				Address:   workerAddress,
				Load:      ws.currentLoad,
				Functions: funcsCopy,
			}
			ws.RUnlock()

			hbBytes, _ := json.Marshal(hb)
			nc.Publish("cluster.heartbeats", hbBytes)
		}
	}()

	// -------------------------------------------------------------
	// TACHES ASYNCHRONES
	// -------------------------------------------------------------
	nc.Subscribe("worker.job."+workerAddress, func(m *nats.Msg) {
		var job map[string]string
		json.Unmarshal(m.Data, &job)
		funcName := job["function"]
		jobID := job["job_id"]

		ws.Lock()
		ws.currentLoad++
		ws.Unlock()

		nic := ws.bootSandbox(funcName)
		fmt.Printf("🔥 [Async] Execution sur NIC %s...\n", nic)
		time.Sleep(500 * time.Millisecond) // Temps de calcul

		rdb.Set(context.Background(), "job:"+jobID, "SUCCESS (Worker: "+workerID+")", 24*time.Hour)

		ws.Lock()
		ws.currentLoad--
		ws.Unlock()
	})

	// -------------------------------------------------------------
	// TACHES SYNCHRONES (HTTP)
	// -------------------------------------------------------------
	http.HandleFunc("/execute/", func(w http.ResponseWriter, r *http.Request) {
		funcName := r.URL.Path[len("/execute/"):]

		ws.Lock()
		ws.currentLoad++
		ws.Unlock()

		nic := ws.bootSandbox(funcName)
		fmt.Printf("🔥 [Sync] Transfert vers NIC %s\n", nic)
		time.Sleep(50 * time.Millisecond) // Simulation de l'execution

		ws.Lock()
		ws.currentLoad--
		ws.Unlock()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status": "success", "worker": "%s", "nic": "%s"}`, workerID, nic)
	})

	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
		log.Printf("Erreur HTTP: %v", err)
	}
}
