package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
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
		IpAddress: "127.0.0.1",
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

	// -------------------------------------------------------------
	// PARTIE 4 : Reverse Proxy HTTP (Attente des requêtes du Data Plane)
	// -------------------------------------------------------------
	// Mimir design: Le Data Plane invoque directement le Worker sans passer par le Controller

	http.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("🔥 [Worker] Requête d'exécution reçue depuis un Data Plane !")

		// Simulation d'un temps de calcul (ex: exécution de la fonction dans une sandbox)
		time.Sleep(50 * time.Millisecond)

		// Renvoi du résultat au Data Plane
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status": "success", "result": "Fonction exécutée par %s"}`, req.WorkerId)
	})

	// Le Worker écoute sur le port 9090 comme déclaré lors de son enregistrement
	fmt.Println("🌐 Worker en écoute sur le port 9090 pour les Data Planes (Data Path)...")
	if err := http.ListenAndServe(":9090", nil); err != nil {
		log.Printf("Erreur du serveur HTTP: %v", err)
	}
}
