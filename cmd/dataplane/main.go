package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "ngfaas/pkg/api" // Import du code généré par Protobuf
)

// main est le point d'entrée du Data Plane.
func main() {
	fmt.Println("🚀 Démarrage du Data Plane ngFaaS...")

	// -------------------------------------------------------------
	// PARTIE 1 : Communication gRPC avec le Controller
	// -------------------------------------------------------------

	// 1. On se connecte au Controller (qui écoute sur le port 50051)
	// On utilise 'insecure' car nous n'avons pas configuré de certificats de sécurité (TLS) pour ce prototype
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("❌ Impossible de se connecter au Controller : %v", err)
	}
	// 'defer' signifie "exécute cette ligne tout à la fin de la fonction main, juste avant de quitter"
	defer conn.Close()

	// 2. On crée le "client" gRPC à partir de la connexion
	client := pb.NewControllerServiceClient(conn)

	// 3. On crée la requête d'enregistrement
	req := &pb.RegisterDataPlaneRequest{
		DataplaneId: "dp-europe-1",
		IpAddress:   "127.0.0.1",
	}

	// 4. On appelle la méthode du Controller (avec un délai maximum de 5 secondes pour la réponse)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	fmt.Println("📞 Envoi de la demande d'enregistrement au Controller...")
	res, err := client.RegisterDataPlane(ctx, req)
	if err != nil {
		log.Fatalf("❌ Erreur lors de l'enregistrement : %v", err)
	}
	fmt.Printf("✅ Réponse du Controller : %s (Succès: %v)\n", res.Message, res.Success)

	// -------------------------------------------------------------
	// PARTIE 2 : Écoute du Queue System NATS
	// -------------------------------------------------------------

	// Connexion à NATS
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf("❌ Impossible de se connecter à NATS : %v", err)
	}
	defer nc.Close()

	// On s'abonne au sujet sur lequel le contrôleur publie les mises à jour
	_, err = nc.Subscribe("workers.updates", func(m *nats.Msg) {
		// Dès qu'on reçoit un message, on l'affiche
		// Dans une vraie app, on mettrait à jour une Map interne "workerID -> Adresse" pour le routage
		fmt.Printf("📥 [DataPlane] Nouvelle info du Controller reçue via NATS : %s\n", string(m.Data))
	})
	if err != nil {
		log.Fatalf("❌ Erreur lors de l'abonnement à NATS : %v", err)
	}
	fmt.Println("🎧 Data Plane abonné aux mises à jour des Workers via NATS.")

	// -------------------------------------------------------------
	// PARTIE 3 : Serveur HTTP (Recevoir les invocations des utilisateurs)
	// -------------------------------------------------------------

	// Simulation d'une route pour invoquer une fonction
	http.HandleFunc("/invoke", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Requête reçue. Simulation de l'envoi vers un Worker...")
	})

	fmt.Println("🌐 Data Plane en écoute sur le port 8080 pour les utilisateurs")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Printf("Erreur du serveur HTTP: %v", err)
	}
}
