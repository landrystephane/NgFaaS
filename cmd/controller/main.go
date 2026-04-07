package main

import (
	"context"
	"fmt"
	"log"
	"net"

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

// RegisterWorker est appelé par un Worker quand il démarre
func (s *server) RegisterWorker(ctx context.Context, req *pb.RegisterWorkerRequest) (*pb.RegisterResponse, error) {
	fmt.Printf("👋 NOUVEAU WORKER ENREGISTRÉ : ID=%s, IP=%s, Port=%d\n", req.WorkerId, req.IpAddress, req.Port)

	// 1. Sauvegarde de l'état dans Redis (Persistance)
	// On crée une clé du type "worker:node-42" et on sauvegarde son IP
	key := fmt.Sprintf("worker:%s", req.WorkerId)
	val := fmt.Sprintf("%s:%d", req.IpAddress, req.Port)

	err := s.rdb.Set(ctx, key, val, 0).Err()
	if err != nil {
		return nil, fmt.Errorf("Erreur lors de la sauvegarde dans Redis: %v", err)
	}
	fmt.Printf("💾 État du Worker %s sauvegardé en base de données.\n", req.WorkerId)

	// 2. Publication via NATS (Queue System) pour prévenir les Data Planes
	msg := fmt.Sprintf(`{"action":"add", "worker_id":"%s", "address":"%s"}`, req.WorkerId, val)
	err = s.nc.Publish("workers.updates", []byte(msg))
	if err != nil {
		fmt.Printf("⚠️ Erreur lors de la publication sur NATS: %v\n", err)
	} else {
		fmt.Printf("📢 Information diffusée sur NATS (sujet: workers.updates)\n")
	}

	return &pb.RegisterResponse{
		Success: true,
		Message: "Worker bien enregistré et sauvegardé en DB par le Controller !",
	}, nil
}

// RegisterDataPlane est appelé par un DataPlane quand il démarre
func (s *server) RegisterDataPlane(ctx context.Context, req *pb.RegisterDataPlaneRequest) (*pb.RegisterResponse, error) {
	fmt.Printf("👋 NOUVEAU DATAPLANE ENREGISTRÉ : ID=%s, IP=%s\n", req.DataplaneId, req.IpAddress)

	// Sauvegarde du Data Plane dans Redis
	key := fmt.Sprintf("dataplane:%s", req.DataplaneId)
	err := s.rdb.Set(ctx, key, req.IpAddress, 0).Err()
	if err != nil {
		return nil, fmt.Errorf("Erreur lors de la sauvegarde dans Redis: %v", err)
	}
	fmt.Printf("💾 État du DataPlane %s sauvegardé en base de données.\n", req.DataplaneId)

	return &pb.RegisterResponse{
		Success: true,
		Message: "DataPlane bien enregistré et sauvegardé en DB par le Controller !",
	}, nil
}

// recoverState permet au contrôleur de récupérer son état depuis Redis après un redémarrage (Crash recovery)
func recoverState(ctx context.Context, rdb *redis.Client) {
	fmt.Println("🔄 Tentative de récupération de l'état depuis la Base de données (Redis)...")

	// On cherche toutes les clés qui commencent par "worker:"
	keys, err := rdb.Keys(ctx, "worker:*").Result()
	if err != nil {
		log.Printf("⚠️ Erreur de récupération: %v\n", err)
		return
	}

	if len(keys) == 0 {
		fmt.Println("ℹ️ Aucun état précédent trouvé. Le cluster démarre à zéro.")
		return
	}

	fmt.Printf("✅ %d Worker(s) retrouvé(s) en base de données :\n", len(keys))
	for _, key := range keys {
		val, _ := rdb.Get(ctx, key).Result()
		fmt.Printf("   -> %s (Adresse: %s)\n", key, val)
	}
}

// main est le point d'entrée du Controller.
func main() {
	fmt.Println("👑 Démarrage du Controller ngFaaS...")

	// -------------------------------------------------------------
	// 1. Connexion à la Base de Données (Redis)
	// -------------------------------------------------------------
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379", // Adresse par défaut de Redis
		Password: "",               // Pas de mot de passe en local
		DB:       0,                // Utiliser la base de données par défaut
	})

	// Vérification de la connexion
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("❌ Impossible de se connecter à Redis. Redis est-il démarré ? Erreur: %v", err)
	}
	fmt.Println("✅ Connecté avec succès à la base de données Redis.")

	// On lance la récupération d'état en cas de redémarrage après une panne
	recoverState(ctx, rdb)

	// -------------------------------------------------------------
	// 2. Connexion à NATS (Queue System)
	// -------------------------------------------------------------
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("❌ Impossible de se connecter à NATS. Le serveur NATS est-il démarré ? Erreur: %v", err)
	}
	defer nc.Close()
	fmt.Println("✅ Connecté avec succès au serveur de messagerie NATS.")

	// -------------------------------------------------------------
	// 3. Démarrage du Serveur gRPC
	// -------------------------------------------------------------

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("❌ Erreur lors de l'ouverture du port : %v", err)
	}

	s := grpc.NewServer()

	// On attache notre logique au serveur gRPC, en lui passant nos clients Redis et NATS
	pb.RegisterControllerServiceServer(s, &server{rdb: rdb, nc: nc})

	fmt.Println("📡 Le Controller écoute les requêtes gRPC sur le port 50051...")

	if err := s.Serve(lis); err != nil {
		log.Fatalf("❌ Erreur du serveur gRPC : %v", err)
	}
}
