package main

import (
	"log"
	"net/http"

	docker "github.com/cloud-pg/interlink/pkg/sidecars/docker"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

func main() {

	commonIL.NewInterLinkConfig()

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", docker.StatusHandler)
	mutex.HandleFunc("/create", docker.CreateHandler)
	mutex.HandleFunc("/delete", docker.DeleteHandler)
	mutex.HandleFunc("/setKubeCFG", docker.SetKubeCFGHandler)

	err := http.ListenAndServe(":"+commonIL.InterLinkConfigInst.Sidecarport, mutex)
	if err != nil {
		log.Fatal(err)
	}
}
