package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type patchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

func handleMutate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var admissionReview admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &admissionReview); err != nil {
		http.Error(w, "failed to unmarshal admission review", http.StatusBadRequest)
		return
	}

	var pod corev1.Pod
	if err := json.Unmarshal(admissionReview.Request.Object.Raw, &pod); err != nil {
		http.Error(w, "failed to unmarshal pod", http.StatusBadRequest)
		return
	}

	// Skip workloads with the skip annotation
	if pod.Annotations != nil {
		if val, ok := pod.Annotations["resource-remover.nais.io/skip"]; ok && val == "true" {
			log.Printf("Skipping %s/%s due to skip annotation", pod.Namespace, pod.Name)
			response := admissionv1.AdmissionReview{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "admission.k8s.io/v1",
					Kind:       "AdmissionReview",
				},
				Response: &admissionv1.AdmissionResponse{
					UID:     admissionReview.Request.UID,
					Allowed: true,
				},
			}
			respBytes, _ := json.Marshal(response)
			w.Header().Set("Content-Type", "application/json")
			w.Write(respBytes)
			return
		}
	}

	var patches []patchOperation

	// Remove safe-to-evict=false annotation if present
	if pod.Annotations != nil {
		if val, ok := pod.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"]; ok && val == "false" {
			patches = append(patches, patchOperation{
				Op:   "remove",
				Path: "/metadata/annotations/cluster-autoscaler.kubernetes.io~1safe-to-evict",
			})
			log.Printf("Removing safe-to-evict=false from %s/%s", pod.Namespace, pod.Name)
		}
	}

	// Remove resource requests AND limits from all containers
	for i, container := range pod.Spec.Containers {
		if container.Resources.Requests != nil {
			if _, hasCPU := container.Resources.Requests[corev1.ResourceCPU]; hasCPU {
				patches = append(patches, patchOperation{
					Op:   "remove",
					Path: fmt.Sprintf("/spec/containers/%d/resources/requests/cpu", i),
				})
			}
			if _, hasMem := container.Resources.Requests[corev1.ResourceMemory]; hasMem {
				patches = append(patches, patchOperation{
					Op:   "remove",
					Path: fmt.Sprintf("/spec/containers/%d/resources/requests/memory", i),
				})
			}
			log.Printf("Removing requests from %s/%s container %s", pod.Namespace, pod.Name, container.Name)
		}
		// Also remove limits since K8s sets requests=limits when limits exist but requests don't
		if container.Resources.Limits != nil {
			if _, hasCPU := container.Resources.Limits[corev1.ResourceCPU]; hasCPU {
				patches = append(patches, patchOperation{
					Op:   "remove",
					Path: fmt.Sprintf("/spec/containers/%d/resources/limits/cpu", i),
				})
			}
			if _, hasMem := container.Resources.Limits[corev1.ResourceMemory]; hasMem {
				patches = append(patches, patchOperation{
					Op:   "remove",
					Path: fmt.Sprintf("/spec/containers/%d/resources/limits/memory", i),
				})
			}
			log.Printf("Removing limits from %s/%s container %s", pod.Namespace, pod.Name, container.Name)
		}
	}

	// Remove resource requests AND limits from all init containers
	for i, container := range pod.Spec.InitContainers {
		if container.Resources.Requests != nil {
			if _, hasCPU := container.Resources.Requests[corev1.ResourceCPU]; hasCPU {
				patches = append(patches, patchOperation{
					Op:   "remove",
					Path: fmt.Sprintf("/spec/initContainers/%d/resources/requests/cpu", i),
				})
			}
			if _, hasMem := container.Resources.Requests[corev1.ResourceMemory]; hasMem {
				patches = append(patches, patchOperation{
					Op:   "remove",
					Path: fmt.Sprintf("/spec/initContainers/%d/resources/requests/memory", i),
				})
			}
			log.Printf("Removing requests from %s/%s init container %s", pod.Namespace, pod.Name, container.Name)
		}
		if container.Resources.Limits != nil {
			if _, hasCPU := container.Resources.Limits[corev1.ResourceCPU]; hasCPU {
				patches = append(patches, patchOperation{
					Op:   "remove",
					Path: fmt.Sprintf("/spec/initContainers/%d/resources/limits/cpu", i),
				})
			}
			if _, hasMem := container.Resources.Limits[corev1.ResourceMemory]; hasMem {
				patches = append(patches, patchOperation{
					Op:   "remove",
					Path: fmt.Sprintf("/spec/initContainers/%d/resources/limits/memory", i),
				})
			}
			log.Printf("Removing limits from %s/%s init container %s", pod.Namespace, pod.Name, container.Name)
		}
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		http.Error(w, "failed to marshal patches", http.StatusInternalServerError)
		return
	}

	log.Printf("Patch for %s/%s: %s", pod.Namespace, pod.Name, string(patchBytes))

	patchType := admissionv1.PatchTypeJSONPatch
	response := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:       admissionReview.Request.UID,
			Allowed:   true,
			PatchType: &patchType,
			Patch:     patchBytes,
		},
	}

	respBytes, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBytes)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8443"
	}

	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")
	if certFile == "" {
		certFile = "/certs/tls.crt"
	}
	if keyFile == "" {
		keyFile = "/certs/tls.key"
	}

	http.HandleFunc("/mutate", handleMutate)
	http.HandleFunc("/healthz", handleHealth)

	log.Printf("Starting resource-request-remover webhook on port %s", port)
	if err := http.ListenAndServeTLS(":"+port, certFile, keyFile, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
