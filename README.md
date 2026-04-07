# Prototype ngFaaS 🚀

Ce projet est un prototype d'architecture de plateforme **Function-as-a-Service (FaaS)** distribuée, développé dans le cadre du projet de recherche **ngFaaS**.

L'architecture est inspirée des concepts mis en avant dans la recherche "Mimir", visant à réduire la latence des *Cold Starts* en décentralisant la prise de décision vers le plan de données (Data Plane).

## 🏗️ Architecture du Système

Le système est composé de trois microservices principaux qui communiquent via **gRPC**, **Redis** et **NATS** :

1. **Le Controller (Control Plane)** : Le cerveau central léger. Il gère l'enregistrement des composants du cluster, persiste l'état dans une base de données Redis (pour la tolérance aux pannes), et diffuse les changements d'état via un système de file de messages (NATS).
2. **Le Data Plane** : Le routeur frontal. Il reçoit les requêtes d'invocation des utilisateurs via une API HTTP REST. Il maintient une vue locale et à jour des ressources disponibles en écoutant le Controller via NATS, permettant un routage quasi instantané.
3. **Le Worker (Nœud d'exécution)** : Le serveur hôte. Il s'enregistre auprès du Controller à son démarrage et sera responsable de l'instanciation des environnements isolés (Sandboxes/Conteneurs) pour exécuter le code utilisateur.

---

## 🛠️ Prérequis et Installation

Pour faire tourner ce projet sur votre machine locale, vous aurez besoin des éléments suivants :

1. **Go (Golang)** : Le langage principal du projet (Version recommandée : 1.22+).
   - [Télécharger Go](https://go.dev/dl/)
2. **Redis** : Base de données clé-valeur en mémoire utilisée pour persister l'état du Controller.
   - *Windows* : Télécharger via [Memurai](https://www.memurai.com/) ou utiliser WSL2 (`sudo apt install redis-server`).
3. **NATS Server** : Le système de messagerie Publish/Subscribe léger.
   - [Télécharger NATS](https://github.com/nats-io/nats-server/releases) (Prendre le fichier `.zip` ou `.tar.gz`, extraire et lancer l'exécutable `nats-server`).

**Initialisation du projet :**
Une fois les dépendances installées, ouvrez votre terminal dans le dossier racine du projet et téléchargez les bibliothèques Go :
```bash
go mod tidy
```

---

## 🚀 Exécution du Projet

Assurez-vous que **Redis** et **NATS Server** tournent en arrière-plan sur leurs ports par défaut (6379 pour Redis, 4222 pour NATS).

Ouvrez trois terminaux distincts à la racine du projet et lancez les composants dans cet ordre :

**Terminal 1 : Le Controller**
```bash
go run ./cmd/controller
```

**Terminal 2 : Le Data Plane**
```bash
go run ./cmd/dataplane
```

**Terminal 3 : Le Worker (Simulation)**
```bash
go run ./cmd/worker
```

---

## 🧪 Comment tester l'invocation d'une fonction ?

Le Data Plane expose un serveur web HTTP sur le port **8080** pour recevoir les requêtes des utilisateurs.

Une fois que les 3 composants tournent, vous pouvez simuler l'invocation d'une fonction (par exemple une fonction nommée "ma-fonction") en envoyant une requête HTTP.

**Avec l'outil `curl` dans un 4ème terminal :**
```bash
curl http://localhost:8080/invoke
```

**Avec votre Navigateur Web :**
Ouvrez simplement votre navigateur et tapez l'URL suivante :
[http://localhost:8080/invoke](http://localhost:8080/invoke)

*(Note : Dans cette phase de prototypage, le Data Plane simule la réception et prépare le routage sans encore exécuter de code réel).*
