# Prototype ngFaaS

Ce projet est un prototype d'architecture de plateforme **Function-as-a-Service (FaaS)** distribuée, développé dans le cadre du projet de recherche **ngFaaS**.

L'architecture est inspirée des concepts mis en avant dans la recherche "Mimir", visant à réduire la latence des *Cold Starts* en décentralisant la prise de décision vers le plan de données (Data Plane).

##  Architecture du Système

Le système est composé de trois microservices principaux qui communiquent via **gRPC**, **Redis** et **NATS** :

1. **Le Controller (Control Plane)** : Le cerveau central léger. Il gère l'enregistrement des composants du cluster, persiste l'état dans une base de données Redis (pour la tolérance aux pannes / Crash Recovery), et diffuse les changements d'état via NATS.
2. **Le Data Plane** : Le routeur frontal intelligent (destiné à être derrière un proxy NGINX). Il maintient une vue locale des ressources disponibles en écoutant le Controller via NATS, permettant un routage quasi instantané (Data Path direct). Il supporte des invocations **Synchrones** et **Asynchrones**.
3. **Le Worker (Simulateur d'exécution)** : Le serveur hôte. Il s'enregistre auprès du Controller à son démarrage. Il contient un "Worker Agent" qui simule l'interface avec un hyperviseur (ex: Firecracker). Il gère une table de routage virtuelle interne (NICs) pour simuler les **Cold Starts** (création de microVM) et les **Warm Starts** (réutilisation).

---

##  Prérequis et Installation

Pour faire tourner ce projet, vous aurez besoin de :

1. **Go (Golang)** (Version 1.22+).
2. **Redis** (Port 6379 par défaut).
3. **NATS Server** (Port 4222 par défaut).

**Initialisation :**
```bash
go mod tidy
```

---

##  Exécution du Projet

Assurez-vous que **Redis** et **NATS Server** tournent.
Lancez les composants dans cet ordre dans des terminaux séparés :

```bash
# Terminal 1 : Le Controller
go run ./cmd/controller

# Terminal 2 : Le Data Plane
go run ./cmd/dataplane

# Terminal 3 : Le Worker (Simulation avec Hyperviseur)
go run ./cmd/worker
```

*(Note : Pour déployer sur le serveur Baremetal Linux, utilisez le script `./test_script.sh` pour compiler les binaires statiques `GOOS=linux`).*

---

##  Comment tester ?

Le Data Plane écoute sur le port **8080**.

**1. Invocation Synchrone (Temps Réel)**
Le navigateur (ou curl) attend la réponse finale du Worker.
```bash
curl http://localhost:8080/invoke/ma-super-fonction
```
*Observez les logs du Worker : Le premier appel déclenchera un "Cold Start", les suivants feront un "Warm Start" instantané.*

**2. Invocation Asynchrone (File d'attente)**
La requête est mise en file, le Data Plane répond immédiatement avec un ID de job.
```bash
curl http://localhost:8080/invoke-async/tache-longue
```

**3. Vérifier le statut (Asynchrone)**
Utilisez le `job_id` retourné à l'étape précédente :
```bash
curl http://localhost:8080/status/{job_id}
```
