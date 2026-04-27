package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	pb "ngfaas/pkg/api"
)

type server struct {
	pb.UnimplementedControllerServiceServer
	rdb *redis.Client
	nc  *nats.Conn
}

func (s *server) RegisterWorker(ctx context.Context, req *pb.RegisterWorkerRequest) (*pb.RegisterResponse, error) {
	fmt.Printf("NOUVEAU WORKER ENREGISTRE : ID=%s, IP=%s, Port=%d\n", req.WorkerId, req.IpAddress, req.Port)

	key := fmt.Sprintf("worker:%s", req.WorkerId)
	val := fmt.Sprintf("%s:%d", req.IpAddress, req.Port)

	s.rdb.Set(ctx, key, val, 0)
	return &pb.RegisterResponse{Success: true}, nil
}

func (s *server) RegisterDataPlane(ctx context.Context, req *pb.RegisterDataPlaneRequest) (*pb.RegisterResponse, error) {
	fmt.Printf("NOUVEAU DATAPLANE ENREGISTRE : ID=%s\n", req.DataplaneId)
	s.rdb.Set(ctx, fmt.Sprintf("dataplane:%s", req.DataplaneId), req.IpAddress, 0)
	return &pb.RegisterResponse{Success: true}, nil
}

func (s *server) RegisterFunction(ctx context.Context, req *pb.RegisterFunctionRequest) (*pb.RegisterResponse, error) {
	return &pb.RegisterResponse{Success: true}, nil
}

func (s *server) SendHeartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	return &pb.HeartbeatResponse{Acknowledged: true}, nil
}

func main() {
	fmt.Println(" Demarrage du Controller ngFaaS (Control Plane & Autoscaler)...")

	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "", DB: 0})
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf(" Redis injoignable: %v", err)
	}

	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatalf(" NATS injoignable: %v", err)
	}
	defer nc.Close()

	// -------------------------------------------------------------
	// AUTOSCALER SIMULE (Ecoute les alertes du DataPlane)
	// -------------------------------------------------------------
	var autoscalerMutex sync.Mutex
	isSpawning := false

	nc.Subscribe("cluster.autoscaler", func(m *nats.Msg) {
		autoscalerMutex.Lock()
		if isSpawning {
			autoscalerMutex.Unlock()
			return // Evite de lancer 50 workers d'un coup
		}
		isSpawning = true
		autoscalerMutex.Unlock()

		fmt.Println("📈 [Autoscaler] Alerte capacite ! Lancement d'un nouveau Worker en cours...")

		go func() {
			// Simulation de demarrage d'hyperviseur/processus
			// On utilise le binaire precompile s'il existe (dans ./build/worker ou ./worker)
			// sinon on fallback sur go run pour le dev local
			var cmd *exec.Cmd

			ex, _ := os.Executable()
			exeDir := filepath.Dir(ex)
			workerBin := filepath.Join(exeDir, "worker")

			if _, err := os.Stat(workerBin); err == nil {
				cmd = exec.Command(workerBin)
			} else {
				// Fallback si on lance via `go run` depuis la racine
				cmd = exec.Command("go", "run", "./cmd/worker/main.go")
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
			}

			err := cmd.Start()
			if err != nil {
				fmt.Printf("Erreur Autoscaler: %v\n", err)
			} else {
				// Pour eviter les processus zombies, on lance une goroutine qui attend la fin du processus
				go func() {
					cmd.Wait()
				}()
			}

			// Delai de grace avant d'autoriser un nouveau spawn
			time.Sleep(3 * time.Second)

			autoscalerMutex.Lock()
			isSpawning = false
			autoscalerMutex.Unlock()
		}()
	})

	// Demarrage Serveur gRPC
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf(" Erreur reseau: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterControllerServiceServer(s, &server{rdb: rdb, nc: nc})

	fmt.Println(" Le Controller ecoute sur le port 50051...")
	if err := s.Serve(lis); err != nil {
		log.Fatalf(" Erreur gRPC: %v", err)
	}
}
