package main

import (
	"fmt"
	"time"
)

// main est le point d'entrée du Controller.
// Son rôle (selon le schéma de Mimir) est de gérer l'enregistrement des Data Planes (flèche 3)
// et des Workers (flèche 5), et de publier les changements d'état via le Queue System (flèche 1).
func main() {
	fmt.Println("👑 Démarrage du Controller ngFaaS...")

	// Dans une vraie implémentation, nous aurions ici un serveur gRPC
	// pour écouter les enregistrements (Data Planes et Workers).

	// Simulation du fonctionnement continu du Controller
	for {
		fmt.Println("📡 Le Controller écoute les enregistrements et surveille l'état du cluster...")
		time.Sleep(5 * time.Second) // Pause de 5 secondes pour ne pas surcharger la console
	}
}
