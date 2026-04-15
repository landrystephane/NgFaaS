package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// RoutingTable garde en mémoire les adresses des Workers disponibles
type RoutingTable struct {
	sync.RWMutex
	workers map[string]string // map[worker_id]ip:port
}

// ClusterUpdate représente un message NATS venant du Controller
type ClusterUpdate struct {
	Action  string `json:"action"` // "add" ou "remove"
	Type    string `json:"type"`   // "worker" ou "function"
	ID      string `json:"id"`
	Address string `json:"address"`
}

func main() {
	routingTable := &RoutingTable{
		workers: make(map[string]string),
	}

	fmt.Println("🚀 Démarrage du Data Plane ngFaaS...")

	// 1. Connexion Redis (pour stocker les résultats des tâches asynchrones)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "", DB: 0})

	// 2. Connexion gRPC au Controller
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("❌ Impossible de se connecter au Controller : %v", err)
	}
	defer conn.Close()
	client := pb.NewControllerServiceClient(conn)

	// Enregistrement du DP
	dpID := "dp-" + uuid.New().String()[:8]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = client.RegisterDataPlane(ctx, &pb.RegisterDataPlaneRequest{DataplaneId: dpID, IpAddress: "127.0.0.1"})
	if err != nil {
		log.Fatalf("❌ Erreur d'enregistrement DP: %v", err)
	}

	// 3. Connexion NATS (pour recevoir les mises à jour du cluster en temps réel)
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("❌ Impossible de se connecter à NATS : %v", err)
	}
	defer nc.Close()

	nc.Subscribe("cluster.updates", func(m *nats.Msg) {
		var update ClusterUpdate
		if err := json.Unmarshal(m.Data, &update); err != nil {
			return
		}

		if update.Type == "worker" && update.Action == "add" {
			routingTable.Lock()
			routingTable.workers[update.ID] = update.Address
			fmt.Printf("📥 [DataPlane] Nouveau Worker détecté via NATS : %s -> %s\n", update.ID, update.Address)
			routingTable.Unlock()
		}
	})

	// -------------------------------------------------------------
	// API HTTP DU DATA PLANE (Le point d'entrée derrière NGINX)
	// -------------------------------------------------------------

	// A. INVOCATION SYNCHRONE : Attend que la fonction se termine
	http.HandleFunc("/invoke/", func(w http.ResponseWriter, r *http.Request) {
		funcName := r.URL.Path[len("/invoke/"):]

		routingTable.RLock()
		var targetAddress string
		for _, addr := range routingTable.workers {
			targetAddress = addr
			break // Stratégie simple
		}
		routingTable.RUnlock()

		if targetAddress == "" {
			http.Error(w, "Aucun Worker disponible", http.StatusServiceUnavailable)
			return
		}

		fmt.Printf("🎯 [Sync] Routage de '%s' vers le Worker %s\n", funcName, targetAddress)

		// Appel direct au Worker (Data Path Mimir)
		workerURL := fmt.Sprintf("http://%s/execute/%s", targetAddress, funcName)
		resp, err := http.Post(workerURL, "application/json", r.Body)
		if err != nil {
			http.Error(w, "Erreur de communication avec le Worker", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	// B. INVOCATION ASYNCHRONE : Répond de suite, place la tâche dans Redis/NATS
	http.HandleFunc("/invoke-async/", func(w http.ResponseWriter, r *http.Request) {
		funcName := r.URL.Path[len("/invoke-async/"):]
		jobID := uuid.New().String()

		// On marque le job comme "En attente" dans Redis
		rdb.Set(context.Background(), "job:"+jobID, "PENDING", 24*time.Hour)

		// On publie la requête sur NATS pour qu'un Worker la récupère (File d'attente)
		jobData := map[string]string{"job_id": jobID, "function": funcName}
		jobBytes, _ := json.Marshal(jobData)
		nc.Publish("async.jobs", jobBytes)

		fmt.Printf("📦 [Async] Tâche asynchrone '%s' acceptée. ID: %s\n", funcName, jobID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"message": "Requête acceptée. Vérifiez le statut plus tard.", "job_id": "%s"}`, jobID)
	})

	// C. VÉRIFICATION DE STATUT (Pour les appels asynchrones)
	http.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.URL.Path[len("/status/"):]

		status, err := rdb.Get(context.Background(), "job:"+jobID).Result()
		if err == redis.Nil {
			http.Error(w, "Job introuvable", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"job_id": "%s", "status": "%s"}`, jobID, status)
	})

	fmt.Println("🌐 Data Plane en écoute sur le port 8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Printf("Erreur du serveur HTTP: %v", err)
	}
}
