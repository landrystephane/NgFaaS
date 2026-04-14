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

	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "ngfaas/pkg/api" // Import du code généré par Protobuf
)

// RoutingTable garde en mémoire les adresses des Workers pour un routage direct
type RoutingTable struct {
	sync.RWMutex
	workers map[string]string // map[worker_id]ip:port
}

// WorkerUpdate représente le message JSON envoyé par le Controller via NATS
type WorkerUpdate struct {
	Action   string `json:"action"`
	WorkerID string `json:"worker_id"`
	Address  string `json:"address"`
}

// main est le point d'entrée du Data Plane.
func main() {
	routingTable := &RoutingTable{
		workers: make(map[string]string),
	}

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
		var update WorkerUpdate
		if err := json.Unmarshal(m.Data, &update); err != nil {
			log.Printf("⚠️ Erreur de parsing du message NATS : %v", err)
			return
		}

		// On met à jour la table de routage locale (Decentralized Scheduling de Mimir)
		routingTable.Lock()
		if update.Action == "add" {
			routingTable.workers[update.WorkerID] = update.Address
			fmt.Printf("📥 [DataPlane] Table de routage mise à jour via NATS : %s -> %s\n", update.WorkerID, update.Address)
		}
		routingTable.Unlock()
	})
	if err != nil {
		log.Fatalf("❌ Erreur lors de l'abonnement à NATS : %v", err)
	}
	fmt.Println("🎧 Data Plane abonné aux mises à jour des Workers via NATS.")

	// -------------------------------------------------------------
	// PARTIE 3 : Serveur HTTP (Recevoir les invocations des utilisateurs)
	// -------------------------------------------------------------

	// Route d'invocation (Data Path) : le client appelle /invoke
	http.HandleFunc("/invoke", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("🌐 [DataPlane] Requête HTTP reçue sur /invoke")

		// 1. Choix d'un worker dans la table de routage (Decentralized Scheduling)
		routingTable.RLock()
		var targetAddress string
		for _, addr := range routingTable.workers {
			targetAddress = addr
			break // Stratégie simple pour le prototype : on prend le premier disponible
		}
		routingTable.RUnlock()

		if targetAddress == "" {
			http.Error(w, "Aucun Worker disponible pour traiter la requête", http.StatusServiceUnavailable)
			return
		}

		fmt.Printf("🎯 [DataPlane] Routage direct vers le Worker à l'adresse %s\n", targetAddress)

		// 2. Invocation directe du Worker via HTTP (comme décrit dans le papier Mimir)
		workerURL := fmt.Sprintf("http://%s/execute", targetAddress)
		resp, err := http.Post(workerURL, "application/json", r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Erreur lors de la communication avec le Worker : %v", err), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		// 3. Renvoi de la réponse du Worker à l'utilisateur
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "Erreur lors de la lecture de la réponse", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(bodyBytes)
		fmt.Println("✅ [DataPlane] Réponse du Worker transférée à l'utilisateur")
	})

	fmt.Println("🌐 Data Plane en écoute sur le port 8080 pour les utilisateurs")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Printf("Erreur du serveur HTTP: %v", err)
	}
}
