package main

import (
	"context"
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"
	pb "ngfaas/pkg/api" // Import du code généré par Protobuf
)

// server implémente l'interface ControllerService définie dans notre fichier .proto
type server struct {
	pb.UnimplementedControllerServiceServer
}

// RegisterWorker est appelé par un Worker quand il démarre
func (s *server) RegisterWorker(ctx context.Context, req *pb.RegisterWorkerRequest) (*pb.RegisterResponse, error) {
	fmt.Printf("👋 NOUVEAU WORKER ENREGISTRÉ : ID=%s, IP=%s, Port=%d\n", req.WorkerId, req.IpAddress, req.Port)

	// Dans le futur, on enregistrera ces données dans Redis ou on les publiera sur NATS
	return &pb.RegisterResponse{
		Success: true,
		Message: "Worker bien enregistré par le Controller !",
	}, nil
}

// RegisterDataPlane est appelé par un DataPlane quand il démarre
func (s *server) RegisterDataPlane(ctx context.Context, req *pb.RegisterDataPlaneRequest) (*pb.RegisterResponse, error) {
	fmt.Printf("👋 NOUVEAU DATAPLANE ENREGISTRÉ : ID=%s, IP=%s\n", req.DataplaneId, req.IpAddress)

	return &pb.RegisterResponse{
		Success: true,
		Message: "DataPlane bien enregistré par le Controller !",
	}, nil
}

// main est le point d'entrée du Controller.
func main() {
	fmt.Println("👑 Démarrage du Controller ngFaaS...")

	// 1. On ouvre un port réseau (TCP sur 50051, le port standard de gRPC)
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("❌ Erreur lors de l'ouverture du port : %v", err)
	}

	// 2. On crée un nouveau serveur gRPC
	s := grpc.NewServer()

	// 3. On attache notre logique (le "server" qu'on a défini au-dessus) au serveur gRPC
	pb.RegisterControllerServiceServer(s, &server{})

	fmt.Println("📡 Le Controller écoute les requêtes gRPC sur le port 50051...")

	// 4. On lance l'écoute infinie
	if err := s.Serve(lis); err != nil {
		log.Fatalf("❌ Erreur du serveur gRPC : %v", err)
	}
}
