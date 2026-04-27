package main

import (
	"context"
	"encoding/json"
	"fmt"
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

// WorkerMeta contient les informations d'un worker
type WorkerMeta struct {
	ID        string
	Address   string
	Functions map[string]string // map[function_name]nic_address
	Load      int
	LastSeen  time.Time
}

// RoutingTable est la memoire du DataPlane
type RoutingTable struct {
	sync.RWMutex
	workers map[string]*WorkerMeta
}

// HeartbeatMsg correspond au format JSON envoye par les Workers
type HeartbeatMsg struct {
	WorkerID  string            `json:"worker_id"`
	Address   string            `json:"address"`
	Load      int               `json:"load"`
	Functions map[string]string `json:"functions"`
}

// ClientConn wrappe le client et la connexion pour permettre une fermeture propre
type ClientConn struct {
	Client pb.WorkerServiceClient
	Conn   *grpc.ClientConn
}

// ConnPool maintient les connexions gRPC ouvertes vers les Workers pour des performances extremes
var (
	connPoolMutex sync.RWMutex
	workerClients = make(map[string]ClientConn)
)

func getWorkerClient(address string) (pb.WorkerServiceClient, error) {
	connPoolMutex.RLock()
	cc, exists := workerClients[address]
	connPoolMutex.RUnlock()

	if exists {
		return cc.Client, nil
	}

	connPoolMutex.Lock()
	defer connPoolMutex.Unlock()
	// Double-check locking
	if cc, exists := workerClients[address]; exists {
		return cc.Client, nil
	}

	fmt.Printf("[DataPlane] Etablissement d'une nouvelle connexion gRPC persistante vers %s\n", address)
	conn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	newClient := pb.NewWorkerServiceClient(conn)
	workerClients[address] = ClientConn{Client: newClient, Conn: conn}
	return newClient, nil
}

func main() {
	routingTable := &RoutingTable{
		workers: make(map[string]*WorkerMeta),
	}

	dpID := "dp-" + uuid.New().String()[:8]
	port := os.Getenv("DP_PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Demarrage du Data Plane %s sur le port %s (HTTP -> gRPC Bridge)...\n", dpID, port)

	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "", DB: 0})

	// Connexion au Controller (gRPC)
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf(" Impossible de se connecter au Controller : %v", err)
	}
	defer conn.Close()
	client := pb.NewControllerServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = client.RegisterDataPlane(ctx, &pb.RegisterDataPlaneRequest{DataplaneId: dpID, IpAddress: "127.0.0.1"})
	if err != nil {
		log.Fatalf(" Erreur enregistrement DP: %v", err)
	}

	// Connexion NATS
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf(" Impossible de se connecter a NATS : %v", err)
	}
	defer nc.Close()

	// -----------------------------------------------------------------------------------
	// INTELLIGENCE DE ROUTAGE : Ecoute des Heartbeats
	// -----------------------------------------------------------------------------------
	nc.Subscribe("cluster.heartbeats", func(m *nats.Msg) {
		var hb HeartbeatMsg
		if err := json.Unmarshal(m.Data, &hb); err != nil {
			return
		}

		routingTable.Lock()
		if _, exists := routingTable.workers[hb.WorkerID]; !exists {
			fmt.Printf("[DataPlane %s] Nouveau Worker detecte: %s\n", dpID, hb.WorkerID)
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

	go func() {
		for {
			time.Sleep(5 * time.Second)
			routingTable.Lock()
			now := time.Now()
			for id, meta := range routingTable.workers {
				if now.Sub(meta.LastSeen) > 10*time.Second {
					fmt.Printf("[DataPlane %s] Worker %s injoignable. Retrait.\n", dpID, id)
					delete(routingTable.workers, id)

					// Nettoyage de la connexion gRPC
					connPoolMutex.Lock()
					if cc, exists := workerClients[meta.Address]; exists {
						cc.Conn.Close()
						delete(workerClients, meta.Address)
					}
					connPoolMutex.Unlock()
				}
			}
			routingTable.Unlock()
		}
	}()

	// -----------------------------------------------------------------------------------
	// LOGIQUE DE SCHEDULING
	// -----------------------------------------------------------------------------------
	findBestWorker := func(funcName string) string {
		routingTable.RLock()
		defer routingTable.RUnlock()

		var bestAddress string
		var lowestLoad = 99999

		// 1. Warm Start prioritaire
		for _, worker := range routingTable.workers {
			if _, hasFunc := worker.Functions[funcName]; hasFunc {
				fmt.Printf("[Routing] Decision: Warm Start sur %s pour '%s'\n", worker.ID, funcName)
				return worker.Address
			}
		}

		// 2. Cold Start sur le moins charge
		for _, worker := range routingTable.workers {
			if worker.Load < lowestLoad {
				lowestLoad = worker.Load
				bestAddress = worker.Address
			}
		}

		if bestAddress != "" {
			fmt.Printf("[Routing] Decision: Cold Start sur le worker %s\n", bestAddress)
		}

		return bestAddress
	}

	// -------------------------------------------------------------
	// API HTTP DU DATA PLANE (Le pont entre le client Web et le reseau gRPC)
	// -------------------------------------------------------------

	// A. INVOCATION SYNCHRONE (Passe maintenant par gRPC pour contacter le Worker)
	http.HandleFunc("/invoke/", func(w http.ResponseWriter, r *http.Request) {
		funcName := r.URL.Path[len("/invoke/"):]

		targetAddress := findBestWorker(funcName)

		if targetAddress == "" {
			nc.Publish("cluster.autoscaler", []byte("Besoin d'un nouveau worker !"))
			http.Error(w, `{"error": "Aucun Worker disponible. Autoscaler prevenu."}`, http.StatusServiceUnavailable)
			return
		}

		// Recuperation du client gRPC pour ce Worker
		workerClient, err := getWorkerClient(targetAddress)
		if err != nil {
			http.Error(w, `{"error": "Erreur interne (gRPC Client)"}`, http.StatusInternalServerError)
			return
		}

		// Appel gRPC ultra-rapide
		invokeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		resp, err := workerClient.InvokeFunction(invokeCtx, &pb.InvokeRequest{
			FunctionName: funcName,
			Payload:      []byte("{}"), // Payload HTTP a passer ici en production
		})

		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": "Echec gRPC vers Worker: %v"}`, err), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if resp.Success {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}

		w.Write([]byte(resp.Result))
	})

	// B. INVOCATION ASYNCHRONE
	http.HandleFunc("/invoke-async/", func(w http.ResponseWriter, r *http.Request) {
		funcName := r.URL.Path[len("/invoke-async/"):]
		jobID := uuid.New().String()

		rdb.Set(context.Background(), "job:"+jobID, "PENDING", 24*time.Hour)

		targetAddress := findBestWorker(funcName)
		if targetAddress == "" {
			nc.Publish("cluster.autoscaler", []byte("Besoin d'un nouveau worker !"))
			http.Error(w, `{"error": "Aucun Worker disponible"}`, http.StatusServiceUnavailable)
			return
		}

		jobData := map[string]string{"job_id": jobID, "function": funcName}
		jobBytes, _ := json.Marshal(jobData)
		nc.Publish("worker.job."+targetAddress, jobBytes)

		fmt.Printf("[Async] Tache '%s' allouee au worker %s. ID: %s\n", funcName, targetAddress, jobID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"message": "Requete asynchrone acceptee.", "job_id": "%s"}`, jobID)
	})

	http.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.URL.Path[len("/status/"):]
		status, err := rdb.Get(context.Background(), "job:"+jobID).Result()
		if err == redis.Nil {
			http.Error(w, `{"error": "Job introuvable"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"job_id": "%s", "status": "%s"}`, jobID, status)
	})

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Erreur du serveur HTTP: %v", err)
	}
}
