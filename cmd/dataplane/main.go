package main

import (
	"fmt"
	"log"
	"net/http"
)

// main est le point d'entrée du Data Plane.
// Son rôle est de recevoir les requêtes des utilisateurs (invocations de fonctions),
// de trouver un Worker disponible (en consultant son état interne mis à jour via le Queue System),
// et de transférer la requête directement au Worker (flèche 4 sur le schéma).
func main() {
	fmt.Println("🚀 Démarrage du Data Plane ngFaaS...")

	// Simulation d'une route pour invoquer une fonction
	http.HandleFunc("/invoke", func(w http.ResponseWriter, r *http.Request) {
		// Ici, le Data Plane devrait :
		// 1. Lire le nom de la fonction depuis la requête
		// 2. Vérifier quel Worker peut l'exécuter
		// 3. Rediriger la requête vers ce Worker
		fmt.Fprintf(w, "Requête reçue. Simulation de l'envoi vers un Worker...")
	})

	fmt.Println("🌐 Data Plane en écoute sur le port 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
