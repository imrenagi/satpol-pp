package pod

import (
	"strconv"

	"github.com/imrenagi/satpol-pp/server/agent"
	corev1 "k8s.io/api/core/v1"
)

// Agent is the top level structure holding all the
// configurations for the agent which validates the pod
type Agent struct {
	Annotations map[string]string
}

// New creates a new instance of Agent by parsing all the Kubernetes annotations.
func New(pod *corev1.Pod) (*Agent, error) {
	agent := &Agent{
		Annotations: pod.Annotations,
	}
	return agent, nil
}

// ShouldIgnore ignore this pod from validation
func ShouldIgnore(pod corev1.Pod) (bool, error) {
	raw, ok := pod.Annotations[agent.AnnotationIgnorePodCheck]
	if !ok {
		return false, nil
	}

	inject, err := strconv.ParseBool(raw)
	if err != nil {
		return false, err
	}

	return inject, nil
}
