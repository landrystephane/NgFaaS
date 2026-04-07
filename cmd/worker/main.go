package main

import (
	"fmt"
	"time"
)

// main est le point d'entrée du Worker simulé.
// Comme discuté avec l'utilisateur, on ne se concentre pas sur la logique réelle des conteneurs ici.
// Ce Worker est juste un "mock" (une simulation) pour tester la communication avec le Data Plane et le Controller.
func main() {
	fmt.Println("⚙️ Démarrage du Worker simulé ngFaaS...")

	// Dans l'architecture Mimir, le Worker s'enregistre auprès du Controller (flèche 5)
	// et écoute les requêtes d'invocation provenant du Data Plane (flèche 4).

	// Simulation du fonctionnement continu
	for {
		fmt.Println("💤 Worker simulé en attente de requêtes...")
		time.Sleep(10 * time.Second)
	}
}
