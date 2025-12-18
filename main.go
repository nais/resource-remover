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

	// Reduce resource requests to 1/5 (20%) and remove limits from all containers
	for i, container := range pod.Spec.Containers {
		if container.Resources.Requests != nil {
			if cpu, hasCPU := container.Resources.Requests[corev1.ResourceCPU]; hasCPU {
				reducedCPU := cpu.MilliValue() / 5
				if reducedCPU < 1 {
					reducedCPU = 1
				}
				patches = append(patches, patchOperation{
					Op:    "replace",
					Path:  fmt.Sprintf("/spec/containers/%d/resources/requests/cpu", i),
					Value: fmt.Sprintf("%dm", reducedCPU),
				})
			}
			if mem, hasMem := container.Resources.Requests[corev1.ResourceMemory]; hasMem {
				reducedMem := mem.Value() / 5
				if reducedMem < 1024*1024 {
					reducedMem = 1024 * 1024 // minimum 1Mi
				}
				patches = append(patches, patchOperation{
					Op:    "replace",
					Path:  fmt.Sprintf("/spec/containers/%d/resources/requests/memory", i),
					Value: fmt.Sprintf("%d", reducedMem),
				})
			}
			log.Printf("Reducing requests to 20%% for %s/%s container %s", pod.Namespace, pod.Name, container.Name)
		}
		// Remove limits so pods aren't throttled
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

	// Reduce resource requests to 20% and remove limits from all init containers
	for i, container := range pod.Spec.InitContainers {
		if container.Resources.Requests != nil {
			if cpu, hasCPU := container.Resources.Requests[corev1.ResourceCPU]; hasCPU {
				reducedCPU := cpu.MilliValue() / 5
				if reducedCPU < 1 {
					reducedCPU = 1
				}
				patches = append(patches, patchOperation{
					Op:    "replace",
					Path:  fmt.Sprintf("/spec/initContainers/%d/resources/requests/cpu", i),
					Value: fmt.Sprintf("%dm", reducedCPU),
				})
			}
			if mem, hasMem := container.Resources.Requests[corev1.ResourceMemory]; hasMem {
				reducedMem := mem.Value() / 5
				if reducedMem < 1024*1024 {
					reducedMem = 1024 * 1024 // minimum 1Mi
				}
				patches = append(patches, patchOperation{
					Op:    "replace",
					Path:  fmt.Sprintf("/spec/initContainers/%d/resources/requests/memory", i),
					Value: fmt.Sprintf("%d", reducedMem),
				})
			}
			log.Printf("Reducing requests to 20%% for %s/%s init container %s", pod.Namespace, pod.Name, container.Name)
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

func handleMutateHPA(w http.ResponseWriter, r *http.Request) {
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

	// Parse HPA to check for skip annotation and get minReplicas
	var hpa struct {
		Metadata struct {
			Name        string            `json:"name"`
			Namespace   string            `json:"namespace"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			MinReplicas *int32 `json:"minReplicas"`
			MaxReplicas int32  `json:"maxReplicas"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(admissionReview.Request.Object.Raw, &hpa); err != nil {
		http.Error(w, "failed to unmarshal hpa", http.StatusBadRequest)
		return
	}

	// Check for skip annotation
	if val, ok := hpa.Metadata.Annotations["resource-remover.nais.io/skip"]; ok && val == "true" {
		log.Printf("Skipping HPA %s/%s due to skip annotation", hpa.Metadata.Namespace, hpa.Metadata.Name)
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

	// Set minReplicas=1 and maxReplicas=1 to disable scaling
	var patches []patchOperation

	if hpa.Spec.MinReplicas == nil {
		patches = append(patches, patchOperation{
			Op:    "add",
			Path:  "/spec/minReplicas",
			Value: 1,
		})
	} else if *hpa.Spec.MinReplicas != 1 {
		patches = append(patches, patchOperation{
			Op:    "replace",
			Path:  "/spec/minReplicas",
			Value: 1,
		})
	}

	if hpa.Spec.MaxReplicas != 1 {
		patches = append(patches, patchOperation{
			Op:    "replace",
			Path:  "/spec/maxReplicas",
			Value: 1,
		})
	}

	if len(patches) > 0 {
		log.Printf("Disabling HPA %s/%s by setting min/maxReplicas=1", hpa.Metadata.Namespace, hpa.Metadata.Name)
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		http.Error(w, "failed to marshal patches", http.StatusInternalServerError)
		return
	}

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

func handleMutateReplicas(w http.ResponseWriter, r *http.Request) {
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

	// Parse workload to check for skip annotation and get replicas
	var workload struct {
		Metadata struct {
			Name        string            `json:"name"`
			Namespace   string            `json:"namespace"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			Replicas *int32 `json:"replicas"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(admissionReview.Request.Object.Raw, &workload); err != nil {
		http.Error(w, "failed to unmarshal workload", http.StatusBadRequest)
		return
	}

	kind := admissionReview.Request.Kind.Kind

	// Check for skip annotation
	if val, ok := workload.Metadata.Annotations["resource-remover.nais.io/skip"]; ok && val == "true" {
		log.Printf("Skipping %s %s/%s due to skip annotation", kind, workload.Metadata.Namespace, workload.Metadata.Name)
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

	var patches []patchOperation

	// Set replicas to 1
	if workload.Spec.Replicas == nil {
		patches = append(patches, patchOperation{
			Op:    "add",
			Path:  "/spec/replicas",
			Value: 1,
		})
	} else if *workload.Spec.Replicas != 1 {
		patches = append(patches, patchOperation{
			Op:    "replace",
			Path:  "/spec/replicas",
			Value: 1,
		})
	}

	if len(patches) > 0 {
		log.Printf("Setting %s %s/%s replicas to 1", kind, workload.Metadata.Namespace, workload.Metadata.Name)
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		http.Error(w, "failed to marshal patches", http.StatusInternalServerError)
		return
	}

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
	http.HandleFunc("/mutate-hpa", handleMutateHPA)
	http.HandleFunc("/mutate-replicas", handleMutateReplicas)
	http.HandleFunc("/healthz", handleHealth)

	log.Printf("Starting resource-request-remover webhook on port %s", port)
	if err := http.ListenAndServeTLS(":"+port, certFile, keyFile, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
