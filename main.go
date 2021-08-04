package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
)

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/", HomeHandler).Methods("GET")
	r.HandleFunc("/apply/{uuid}", ApplyConfig).Methods("POST")
	http.Handle("/", r)

	http.ListenAndServe(":8080", r)
}

func HomeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("OK")
	w.WriteHeader(http.StatusOK)
}

type tfConfig struct {
	Config string `json:"config"`
}

func ApplyConfig(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	uuid := vars["uuid"]
	fmt.Println(uuid)
	config := &tfConfig{}

	err := json.NewDecoder(r.Body).Decode(config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fmt.Println("CONFIG")
	fmt.Printf("%v", config)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Category: %v\n", uuid)
}
