package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "ngfaas/pkg/api"
)

// main est le point d'entrée du Worker simulé.
func main() {
	fmt.Println("⚙️ Démarrage du Worker simulé ngFaaS...")

	// 1. On se connecte au Controller
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("❌ Impossible de se connecter au Controller : %v", err)
	}
	defer conn.Close()

	// 2. Client gRPC
	client := pb.NewControllerServiceClient(conn)

	// 3. Requête d'enregistrement du Worker
	req := &pb.RegisterWorkerRequest{
		WorkerId:  "worker-node-42",
		IpAddress: "192.168.1.100",
		Port:      9090, // Le port sur lequel ce Worker écouterait les DataPlanes
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	fmt.Println("📞 Envoi de la demande d'enregistrement au Controller...")
	res, err := client.RegisterWorker(ctx, req)
	if err != nil {
		log.Fatalf("❌ Erreur lors de l'enregistrement : %v", err)
	}
	fmt.Printf("✅ Réponse du Controller : %s (Succès: %v)\n", res.Message, res.Success)

	// Simulation du fonctionnement continu
	for {
		fmt.Println("💤 Worker simulé en attente de requêtes (mock)...")
		time.Sleep(10 * time.Second)
	}
}
