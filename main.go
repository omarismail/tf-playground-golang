package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	tfe "github.com/hashicorp/go-tfe"
	lru "github.com/hashicorp/golang-lru"
)

var (
	// key must be 16, 24 or 32 bytes long (AES-128, AES-192 or AES-256)
	key   = []byte("super-secret-key")
	store = sessions.NewCookieStore(key)
)

const (
	playgroundSession = "playground-session"
	playgroundOrg     = "terraform-playground-hack"
)

type server struct {
	tfeClient *tfe.Client
	ctx       context.Context
	cache     *lru.ARCCache
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
	newCache, err := lru.NewARC(1000)
	if err != nil {
		log.Fatalf("could not create tfe client %v", err)
	}
	s := &server{
		tfeClient: client,
		ctx:       context.Background(),
		cache:     newCache,
	}

	r := mux.NewRouter()
	r.HandleFunc("/", s.HomeHandler).Methods("GET")
	r.HandleFunc("/favicon.ico", faviconHandler).Methods("GET")
	r.HandleFunc("/apply/{uuid}", s.ApplyConfig).Methods("POST")
	r.HandleFunc("/runs/{id}", s.RunHandler).Methods("GET")

	// Choose the folder to serve
	staticDir := "/assets/"
	// Create the route
	r.
		PathPrefix(staticDir).
		Handler(http.StripPrefix(staticDir, http.FileServer(http.Dir("."+staticDir))))
	http.Handle("/", r)

	http.ListenAndServe(":8080", r)
}
func faviconHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "./assets/favicon.ico")
}

type PageData struct {
	Uuid        string
	ApplyPath   string
	RunId       string
	StateOutput string
	Config      string
	RunData     runOutput
}

type runOutput struct {
	RunId  string
	Output string
	Config string
}

func (s *server) HomeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("OK")
	sessionUUID, err := getSessionUuid(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Println(sessionUUID)

	tmpl := template.Must(template.ParseFiles("layout.html"))
	data := PageData{
		Uuid: sessionUUID,
	}
	if s.cache.Contains(sessionUUID) {
		log.Println("Contains Key", sessionUUID)
		if val, hasKey := s.cache.Get(sessionUUID); hasKey {
			log.Println("Get Key")
			if runVal, ok := val.(*runOutput); ok {
				log.Println("RUNVAL")
				log.Println(runVal)
				data.RunId = runVal.RunId
				data.StateOutput = runVal.Output
				data.Config = runVal.Config
			}
		}
	}

	data.ApplyPath = fmt.Sprintf("/apply/%s", data.Uuid)
	tmpl.Execute(w, data)
}

func getSessionUuid(w http.ResponseWriter, r *http.Request) (string, error) {
	session, err := store.Get(r, playgroundSession)
	if err != nil {
		return "", err
	}
	var id string
	val := session.Values["uuid"]
	id, ok := val.(string)
	if ok {
		return id, nil
	}
	id = uuid.NewString()
	session.Values["uuid"] = id
	err = sessions.Save(r, w)
	if err != nil {
		return "", err
	}
	return id, nil
}

func renderTemplate(w http.ResponseWriter, tmpl string, pd PageData) {
	t := template.Must(template.ParseFiles(tmpl + ".html"))
	t.Execute(w, pd)
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
	configVal := r.FormValue("config")

	log.Println(uuid)
	log.Println(configVal)

	workspace, err := s.findOrCreateWorkspace(uuid)
	if err != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	//config := &tfConfig{}
	//err = json.NewDecoder(r.Body).Decode(config)
	//if err != nil {
	//	log.Printf("%v", err)
	//	http.Error(w, err.Error(), http.StatusBadRequest)
	//	return
	//}
	//fmt.Println("CONFIG")
	//fmt.Printf("%v", config)

	config := &tfConfig{
		Config: configVal,
	}
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
	fmt.Println(run.ID)
	res := &runResponse{
		ID: run.ID,
	}

	jsonResponse, jsonError := json.Marshal(res)
	if jsonError != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	fmt.Println(string(jsonResponse))
	sessionUUID, err := getSessionUuid(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	runData := &runOutput{
		RunId:  run.ID,
		Config: configVal,
	}
	s.cache.Add(sessionUUID, runData)

	pageData := PageData{
		Uuid:      sessionUUID,
		RunId:     run.ID,
		Config:    configVal,
		ApplyPath: fmt.Sprintf("/apply/%s", sessionUUID),
	}
	fmt.Println(pageData)
	//renderTemplate(w, "layout", pageData)
	http.Redirect(w, r, "/", http.StatusFound)

	//w.Header().Set("Content-Type", "application/json")
	//w.WriteHeader(http.StatusOK)
	//w.Write(jsonResponse)
}

type runDetails struct {
	Status  string   `json:"status"`
	Outputs []string `json:"outputs"`
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
	jsonResponse, jsonError := json.Marshal(details)
	if jsonError != nil {
		log.Printf("%v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	fmt.Println(string(jsonResponse))

	fmt.Println("Responding...")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	w.Write(jsonResponse)
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
