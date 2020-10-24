package server

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/hashicorp/vault/helper/strutil"
	cmAgent "github.com/imrenagi/satpol-pp/server/agent/configmap"
	podAgent "github.com/imrenagi/satpol-pp/server/agent/pod"
	"github.com/rs/zerolog"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
)

var (
	deserializer = func() runtime.Decoder {
		codecs := serializer.NewCodecFactory(runtime.NewScheme())
		return codecs.UniversalDeserializer()
	}

	kubeSystemNamespaces = []string{
		metav1.NamespaceSystem,
		metav1.NamespacePublic,
	}
)

// Handler is the HTTP handler for admission webhooks.
type Handler struct {
	Clientset *kubernetes.Clientset
	Log       zerolog.Logger
}

// PodCheckHandler ...
func (h *Handler) PodCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.handle(w, r, h.checkPod)
	}
}

func (h *Handler) checkPod(req *v1beta1.AdmissionRequest) *v1beta1.AdmissionResponse {
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		h.Log.Error().Err(err).Msg("could not unmarshal request to pod")
		h.Log.Debug().Str("raw", string(req.Object.Raw)).Msg("pod manifest")
		return &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	// Build the basic response
	reviewResponse := &v1beta1.AdmissionResponse{
		Allowed: true,
		UID:     req.UID,
	}

	h.Log.Debug().Msg("checking if should ignore this pod")
	ignore, err := podAgent.ShouldIgnore(pod)
	if err != nil && !strings.Contains(err.Error(), "no inject annotation found") {
		err := fmt.Errorf("error checking if should ignore this pod: %s", err)
		return admissionError(err)
	} else if ignore {
		return reviewResponse
	}

	h.Log.Debug().Msg("checking namespaces..")
	if strutil.StrListContains(kubeSystemNamespaces, req.Namespace) {
		reviewResponse.Allowed = true
		return reviewResponse
	}

	whitelistedRegistry := []string{"gcr.io/imre-demo"}

	var validRegistry bool
	for _, container := range pod.Spec.Containers {
		for _, registry := range whitelistedRegistry {
			if strings.Contains(container.Image, registry) {
				validRegistry = true
				break
			}
		}
	}

	if !validRegistry {
		h.Log.Debug().Msg("image registry is not allowed")
		reviewResponse.Allowed = false
		reviewResponse.Result = &metav1.Status{Message: "image registry is not allowed"}
	}
	return reviewResponse
}

// ConfigMapCheckHandler ...
func (h *Handler) ConfigMapCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.handle(w, r, h.checkConfigMap)
	}
}

func (h *Handler) checkConfigMap(req *v1beta1.AdmissionRequest) *v1beta1.AdmissionResponse {

	h.Log.Debug().Msg("executing configmap handler")

	var configmap corev1.ConfigMap
	if err := json.Unmarshal(req.Object.Raw, &configmap); err != nil {
		h.Log.Error().Err(err).Msg("could not unmarshal request to configmap")
		h.Log.Debug().Str("raw", string(req.Object.Raw)).Msg("configmap manifest")
		return &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	// Build the basic response
	reviewResponse := &v1beta1.AdmissionResponse{
		Allowed: true,
		UID:     req.UID,
	}

	h.Log.Debug().Msg("checking if should ignore this configmap")
	check, err := cmAgent.ShouldCheck(configmap)
	if err != nil && !strings.Contains(err.Error(), "no inject annotation found") {
		err := fmt.Errorf("error checking if should ignore this configmap: %s", err)
		return admissionError(err)
	} else if !check {
		return reviewResponse
	}

	h.Log.Debug().Msg("checking namespaces..")
	if strutil.StrListContains(kubeSystemNamespaces, req.Namespace) {
		return reviewResponse
	}

	agentCfg := &cmAgent.AgentConfig{
		GoogleProjectID: "imre-demo",
	}

	agent, err := cmAgent.New(agentCfg)
	if err != nil {
		return admissionError(err)
	}

	err = agent.Validate(configmap)
	if err != nil {
		h.Log.Debug().Msg("configmap is not valid")
		reviewResponse.Allowed = false
		reviewResponse.Result = &metav1.Status{Message: err.Error()}
	}

	return reviewResponse
}

type admissionFunc func(req *v1beta1.AdmissionRequest) *v1beta1.AdmissionResponse

func (h *Handler) handle(w http.ResponseWriter, r *http.Request, fn admissionFunc) {
	h.Log.Info().Str("method", r.Method).Str("method", r.Method).Msg("Request received")

	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		msg := fmt.Sprintf("Invalid content-type: %q", ct)
		http.Error(w, msg, http.StatusBadRequest)
		h.Log.Warn().Str("msg", msg).Int("code", http.StatusBadRequest).Msg("invalid content type")
		return
	}

	var body []byte
	if r.Body != nil {
		var err error
		if body, err = ioutil.ReadAll(r.Body); err != nil {
			msg := fmt.Sprintf("error reading request body: %s", err)
			http.Error(w, msg, http.StatusBadRequest)
			h.Log.Error().Str("msg", msg).Int("code", http.StatusBadRequest).Msg("error reading request body")
			return
		}
	}
	if len(body) == 0 {
		msg := "Empty request body"
		http.Error(w, msg, http.StatusBadRequest)
		h.Log.Error().Str("msg", msg).Int("code", http.StatusBadRequest).Msg("empty request body")
		return
	}

	var admReq v1beta1.AdmissionReview
	var admResp v1beta1.AdmissionReview
	if _, _, err := deserializer().Decode(body, nil, &admReq); err != nil {
		msg := fmt.Sprintf("error decoding admission request: %s", err)
		http.Error(w, msg, http.StatusInternalServerError)
		h.Log.Error().Str("msg", msg).Int("code", http.StatusInternalServerError).Msg("error on request")
		return
	}

	admResp.Response = fn(admReq.Request)

	resp, err := json.Marshal(&admResp)
	if err != nil {
		msg := fmt.Sprintf("error marshalling admission response: %s", err)
		http.Error(w, msg, http.StatusInternalServerError)
		h.Log.Error().Str("msg", msg).Int("code", http.StatusInternalServerError).Msg("error on request")
		return
	}

	if _, err := w.Write(resp); err != nil {
		h.Log.Error().Err(err).Msg("error writing response")
	}
}

func admissionError(err error) *v1beta1.AdmissionResponse {
	return &v1beta1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}
