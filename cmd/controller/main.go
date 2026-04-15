package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	pb "ngfaas/pkg/api" // Import du code généré par Protobuf
)

// server implémente l'interface ControllerService définie dans notre fichier .proto
type server struct {
	pb.UnimplementedControllerServiceServer
	rdb *redis.Client // Client pour communiquer avec la base de données Redis
	nc  *nats.Conn    // Client NATS pour publier les messages
}

// =========================================================================
// 1. GESTION DES WORKERS (FaaS Cluster Mgmt)
// =========================================================================
func (s *server) RegisterWorker(ctx context.Context, req *pb.RegisterWorkerRequest) (*pb.RegisterResponse, error) {
	fmt.Printf("👋 NOUVEAU WORKER : ID=%s, IP=%s, Port=%d\n", req.WorkerId, req.IpAddress, req.Port)

	// Sauvegarde de l'état dans Redis (Persistance)
	key := fmt.Sprintf("worker:%s", req.WorkerId)
	val := fmt.Sprintf("%s:%d", req.IpAddress, req.Port)

	if err := s.rdb.Set(ctx, key, val, 0).Err(); err != nil {
		return nil, fmt.Errorf("Erreur Redis: %v", err)
	}

	// Publication via NATS pour prévenir les Data Planes
	updateMsg := map[string]interface{}{"action": "add", "type": "worker", "id": req.WorkerId, "address": val}
	msgBytes, _ := json.Marshal(updateMsg)
	s.nc.Publish("cluster.updates", msgBytes)

	return &pb.RegisterResponse{Success: true, Message: "Worker enregistré en base."}, nil
}

// =========================================================================
// 2. GESTION DES DATAPLANES (FaaS Cluster Mgmt)
// =========================================================================
func (s *server) RegisterDataPlane(ctx context.Context, req *pb.RegisterDataPlaneRequest) (*pb.RegisterResponse, error) {
	fmt.Printf("👋 NOUVEAU DATAPLANE : ID=%s, IP=%s\n", req.DataplaneId, req.IpAddress)

	key := fmt.Sprintf("dataplane:%s", req.DataplaneId)
	if err := s.rdb.Set(ctx, key, req.IpAddress, 0).Err(); err != nil {
		return nil, fmt.Errorf("Erreur Redis: %v", err)
	}

	return &pb.RegisterResponse{Success: true, Message: "DataPlane enregistré en base."}, nil
}

// =========================================================================
// 3. GESTION DES FONCTIONS (FaaS Control)
// =========================================================================
func (s *server) RegisterFunction(ctx context.Context, req *pb.RegisterFunctionRequest) (*pb.RegisterResponse, error) {
	fmt.Printf("📦 NOUVELLE FONCTION CRÉÉE : Nom=%s, Image=%s\n", req.FunctionName, req.ImageUrl)

	// Sauvegarde de la définition de la fonction dans Redis
	key := fmt.Sprintf("function:%s", req.FunctionName)
	funcData := map[string]interface{}{
		"name":            req.FunctionName,
		"image_url":       req.ImageUrl,
		"memory_limit_mb": req.MemoryLimitMb,
	}
	funcBytes, _ := json.Marshal(funcData)

	if err := s.rdb.Set(ctx, key, funcBytes, 0).Err(); err != nil {
		return nil, fmt.Errorf("Erreur Redis: %v", err)
	}

	// On prévient les Data Planes de l'existence de cette nouvelle fonction
	updateMsg := map[string]interface{}{"action": "add", "type": "function", "name": req.FunctionName}
	msgBytes, _ := json.Marshal(updateMsg)
	s.nc.Publish("cluster.updates", msgBytes)

	return &pb.RegisterResponse{Success: true, Message: "Fonction ajoutée au registre !"}, nil
}

// =========================================================================
// 4. HEARTBEATS (Surveillance du cluster)
// =========================================================================
func (s *server) SendHeartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	// Met à jour un timer d'expiration (TTL) dans Redis.
	// Si on ne reçoit pas de ping d'ici 30 secondes, l'entrée sera effacée (composant considéré comme mort)
	key := fmt.Sprintf("%s:%s", req.ComponentType, req.ComponentId)
	s.rdb.Expire(ctx, key, 30*time.Second)

	return &pb.HeartbeatResponse{Acknowledged: true}, nil
}

// =========================================================================
// RECOVERY (Tolérance aux pannes)
// =========================================================================
// Si le Controller crash, le nouveau Controller relit Redis pour reconstruire son état
func recoverState(ctx context.Context, rdb *redis.Client, nc *nats.Conn) {
	fmt.Println("🔄 Récupération de l'état depuis Redis (Crash Recovery)...")

	keys, _ := rdb.Keys(ctx, "worker:*").Result()
	for _, key := range keys {
		val, _ := rdb.Get(ctx, key).Result()
		workerID := key[7:] // Enlève "worker:"

		// On rediffuse l'état retrouvé aux Data Planes
		updateMsg := map[string]interface{}{"action": "add", "type": "worker", "id": workerID, "address": val}
		msgBytes, _ := json.Marshal(updateMsg)
		nc.Publish("cluster.updates", msgBytes)
	}
	fmt.Printf("✅ %d Worker(s) restauré(s).\n", len(keys))
}

func main() {
	fmt.Println("👑 Démarrage du Controller ngFaaS (Control Plane)...")

	// Connexion Redis
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "", DB: 0})
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("❌ Redis injoignable: %v", err)
	}

	// Connexion NATS
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("❌ NATS injoignable: %v", err)
	}
	defer nc.Close()

	// Récupération d'état
	recoverState(ctx, rdb, nc)

	// Démarrage Serveur gRPC
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("❌ Erreur réseau: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterControllerServiceServer(s, &server{rdb: rdb, nc: nc})

	fmt.Println("📡 Le Controller écoute sur le port 50051...")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("❌ Erreur gRPC: %v", err)
	}
}
