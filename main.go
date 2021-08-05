package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	tfe "github.com/hashicorp/go-tfe"
)

const (
	playgroundOrg = "terraform-playground-hack"
)

type server struct {
	tfeClient *tfe.Client
	ctx       context.Context
}

func main() {
	token := os.Getenv("TFE_TOKEN")
	if token == "" {
		log.Fatalf("did not have token")
	}
	cfg := &tfe.Config{
		Token: token,
	}

	// Create a new TFE client.
	client, err := tfe.NewClient(cfg)
	if err != nil {
		log.Fatalf("could not create tfe client %v", err)
	}
	s := &server{
		tfeClient: client,
		ctx:       context.Background(),
	}

	r := mux.NewRouter()
	r.HandleFunc("/", s.HomeHandler).Methods("GET")
	r.HandleFunc("/apply/{uuid}", s.ApplyConfig).Methods("POST")
	r.HandleFunc("/runs/{id}", s.RunHandler).Methods("GET")
	http.Handle("/", r)

	http.ListenAndServe(":8080", r)
}

func (s *server) HomeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("OK")
	w.WriteHeader(http.StatusOK)
}

type tfConfig struct {
	Config string `json:"config"`
}

type runResponse struct {
	ID string `json:"run-id"`
}

func (s *server) ApplyConfig(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uuid := vars["uuid"]
	fmt.Println(uuid)
	log.Println(uuid)

	workspace, err := s.findOrCreateWorkspace(uuid)
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	config := &tfConfig{}
	err = json.NewDecoder(r.Body).Decode(config)
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fmt.Println("CONFIG")
	fmt.Printf("%v", config)

	pwd, err := os.Getwd()
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dirConfig := fmt.Sprintf("%s/%s", pwd, uuid)
	filePath := fmt.Sprintf("%s/main.tf", dirConfig)
	err = writeConfigToFile(dirConfig, filePath, config)
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	cv, err := s.tfeClient.ConfigurationVersions.Create(
		s.ctx,
		workspace.ID,
		tfe.ConfigurationVersionCreateOptions{
			AutoQueueRuns: tfe.Bool(false),
		},
	)
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	fmt.Println(filePath)
	err = s.uploadConfig(cv, dirConfig)
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	options := tfe.RunCreateOptions{
		Workspace: workspace,
	}

	run, err := s.tfeClient.Runs.Create(s.ctx, options)
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	res := &runResponse{
		ID: run.ID,
	}
	err = json.NewEncoder(w).Encode(res)
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type runDetails struct {
	Status  string   `json:"status"`
	Outputs []string `json:"output-values"`
}

func (s *server) RunHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	runID := vars["id"]
	run, err := s.tfeClient.Runs.ReadWithOptions(s.ctx, runID, &tfe.RunReadOptions{
		Include: "workspace",
	})
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	log.Println(run.Status)

	details := &runDetails{
		Status: string(run.Status),
	}
	outputs := []string{}
	if run.Status == tfe.RunApplied || run.Status == tfe.RunPlannedAndFinished {
		sv, err := s.tfeClient.StateVersions.CurrentWithOptions(s.ctx, run.Workspace.ID, &tfe.StateVersionCurrentOptions{
			Include: "outputs",
		})
		if err != nil {
			log.Printf("%v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, op := range sv.Outputs {
			outputs = append(outputs, op.Value)
		}
	}
	details.Outputs = outputs
	err = json.NewEncoder(w).Encode(details)
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *server) findOrCreateWorkspace(uuid string) (*tfe.Workspace, error) {
	var workspace *tfe.Workspace

	workspace, err := s.tfeClient.Workspaces.Read(s.ctx, playgroundOrg, uuid)
	if err != nil {
		if err == tfe.ErrResourceNotFound {
			workspace, err = s.tfeClient.Workspaces.Create(s.ctx, playgroundOrg, tfe.WorkspaceCreateOptions{
				Name:      tfe.String(uuid),
				AutoApply: tfe.Bool(true),
			})
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	return workspace, nil
}

func (s *server) uploadConfig(cv *tfe.ConfigurationVersion, dir string) error {
	err := s.tfeClient.ConfigurationVersions.Upload(s.ctx, cv.UploadURL, dir)
	if err != nil {
		return err
	}
	for i := 0; ; i++ {
		cv, err = s.tfeClient.ConfigurationVersions.Read(s.ctx, cv.ID)
		if err != nil {
			return err
		}
		log.Println(cv.Status)

		if cv.Status == tfe.ConfigurationUploaded {
			break
			return nil
		}

		if i > 30 {
			return fmt.Errorf("Timeout waiting for the configuration version to be uploaded")
		}

		time.Sleep(1 * time.Second)
	}
	return nil
}

func writeConfigToFile(dir string, filepath string, config *tfConfig) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.Mkdir(dir, 0755)
		if err != nil {
			return err
		}
	}
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(config.Config)

	if err != nil {
		return err
	}
	return nil
}
