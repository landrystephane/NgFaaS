package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "ngfaas/pkg/api"
)

// WorkerMeta contient les informations d'un worker, y compris les fonctions qu'il a déjà en mémoire (Warm Start possible)
type WorkerMeta struct {
	ID        string
	Address   string
	Functions map[string]string // map[function_name]nic_address
	Load      int               // Simule la charge de travail actuelle
	LastSeen  time.Time
}

// RoutingTable est la mémoire du DataPlane, alimentée par NATS
type RoutingTable struct {
	sync.RWMutex
	workers map[string]*WorkerMeta
}

// HeartbeatMsg correspond au format JSON envoyé par les Workers via NATS
type HeartbeatMsg struct {
	WorkerID  string            `json:"worker_id"`
	Address   string            `json:"address"`
	Load      int               `json:"load"`
	Functions map[string]string `json:"functions"`
}

func main() {
	routingTable := &RoutingTable{
		workers: make(map[string]*WorkerMeta),
	}

	dpID := "dp-" + uuid.New().String()[:8]
	port := os.Getenv("DP_PORT")
	if port == "" {
		port = "8080" // Port par défaut, mais surchargeable pour lancer plusieurs instances
	}

	fmt.Printf("🚀 Démarrage du Data Plane %s sur le port %s...\n", dpID, port)

	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "", DB: 0})

	// Connexion au Controller (gRPC) pour enregistrement
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("❌ Impossible de se connecter au Controller : %v", err)
	}
	defer conn.Close()
	client := pb.NewControllerServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = client.RegisterDataPlane(ctx, &pb.RegisterDataPlaneRequest{DataplaneId: dpID, IpAddress: "127.0.0.1"})
	if err != nil {
		log.Fatalf("❌ Erreur d'enregistrement DP: %v", err)
	}

	// Connexion NATS
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("❌ Impossible de se connecter à NATS : %v", err)
	}
	defer nc.Close()

	// -----------------------------------------------------------------------------------
	// INTELLIGENCE MIMIR : Écoute des Heartbeats détaillés pour construire la table de routage
	// -----------------------------------------------------------------------------------
	nc.Subscribe("cluster.heartbeats", func(m *nats.Msg) {
		var hb HeartbeatMsg
		if err := json.Unmarshal(m.Data, &hb); err != nil {
			return
		}

		routingTable.Lock()
		if _, exists := routingTable.workers[hb.WorkerID]; !exists {
			fmt.Printf("📥 [DataPlane %s] Nouveau Worker détecté: %s (Adresses: %s)\n", dpID, hb.WorkerID, hb.Address)
		}
		routingTable.workers[hb.WorkerID] = &WorkerMeta{
			ID:        hb.WorkerID,
			Address:   hb.Address,
			Functions: hb.Functions,
			Load:      hb.Load,
			LastSeen:  time.Now(),
		}
		routingTable.Unlock()
	})

	// Nettoyage périodique des workers morts (TTL de 10 secondes)
	go func() {
		for {
			time.Sleep(5 * time.Second)
			routingTable.Lock()
			now := time.Now()
			for id, meta := range routingTable.workers {
				if now.Sub(meta.LastSeen) > 10*time.Second {
					fmt.Printf("⚠️ [DataPlane %s] Worker %s injoignable. Retrait de la table.\n", dpID, id)
					delete(routingTable.workers, id)
				}
			}
			routingTable.Unlock()
		}
	}()

	// -----------------------------------------------------------------------------------
	// LOGIQUE DE SCHEDULING (La vraie décision Orchestrateur)
	// -----------------------------------------------------------------------------------
	findBestWorker := func(funcName string) string {
		routingTable.RLock()
		defer routingTable.RUnlock()

		var bestAddress string
		var lowestLoad = 99999

		// 1. Chercher un Warm Start (Worker qui a déjà la fonction)
		for _, worker := range routingTable.workers {
			if _, hasFunc := worker.Functions[funcName]; hasFunc {
				fmt.Printf("🧠 [Orchestrateur] Décision: Warm Start possible sur %s pour '%s'\n", worker.ID, funcName)
				return worker.Address
			}
		}

		// 2. Si pas de Warm Start, on cherche le Worker le moins chargé pour un Cold Start
		for _, worker := range routingTable.workers {
			if worker.Load < lowestLoad {
				lowestLoad = worker.Load
				bestAddress = worker.Address
			}
		}

		if bestAddress != "" {
			fmt.Printf("🧠 [Orchestrateur] Décision: Cold Start sur le worker le moins chargé (%s)\n", bestAddress)
		}

		return bestAddress
	}

	// -------------------------------------------------------------
	// API HTTP DU DATA PLANE
	// -------------------------------------------------------------

	// A. INVOCATION SYNCHRONE
	http.HandleFunc("/invoke/", func(w http.ResponseWriter, r *http.Request) {
		funcName := r.URL.Path[len("/invoke/"):]

		targetAddress := findBestWorker(funcName)

		if targetAddress == "" {
			// Simule un appel à l'autoscaler via NATS
			nc.Publish("cluster.autoscaler", []byte("Besoin d'un nouveau worker !"))
			http.Error(w, "Aucun Worker disponible. Autoscaler prévenu.", http.StatusServiceUnavailable)
			return
		}

		// Routage Data Path direct (HTTP)
		workerURL := fmt.Sprintf("http://%s/execute/%s", targetAddress, funcName)
		resp, err := http.Post(workerURL, "application/json", r.Body)
		if err != nil {
			http.Error(w, "Erreur Worker", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	// B. INVOCATION ASYNCHRONE
	http.HandleFunc("/invoke-async/", func(w http.ResponseWriter, r *http.Request) {
		funcName := r.URL.Path[len("/invoke-async/"):]
		jobID := uuid.New().String()

		rdb.Set(context.Background(), "job:"+jobID, "PENDING", 24*time.Hour)

		// Au lieu de jeter au hasard, on alloue la tâche au meilleur Worker et on lui dit directement !
		targetAddress := findBestWorker(funcName)
		if targetAddress == "" {
			nc.Publish("cluster.autoscaler", []byte("Besoin d'un nouveau worker !"))
			http.Error(w, "Aucun Worker disponible", http.StatusServiceUnavailable)
			return
		}

		// On envoie le job de manière asynchrone UNIQUEMENT au worker choisi via une route spécifique NATS
		jobData := map[string]string{"job_id": jobID, "function": funcName}
		jobBytes, _ := json.Marshal(jobData)
		nc.Publish("worker.job."+targetAddress, jobBytes) // Routage déterministe

		fmt.Printf("📦 [Async] Tâche '%s' allouée au worker %s. ID: %s\n", funcName, targetAddress, jobID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"message": "Requête asynchrone acceptée.", "job_id": "%s"}`, jobID)
	})

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

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Erreur du serveur HTTP: %v", err)
	}
}
