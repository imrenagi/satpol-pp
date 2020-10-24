package deployment

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/imrenagi/satpol-pp/server/agent"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

type AgentConfig struct {
	ImageRegistries []string
}

func Init(cfg *AgentConfig) error {
	cfg.ImageRegistries = []string{
		"gcr.io/imre-demo",
		"docker.io/imrenagi",
	}

	return nil
}

// Agent is the top level structure holding all the
// configurations for the agent which validates the deployment
type Agent struct {
	cfg *AgentConfig
}

// New creates a new instance of Agent by parsing all the Kubernetes annotations.
func New(cfg *AgentConfig) (*Agent, error) {
	agent := &Agent{
		cfg: cfg,
	}
	return agent, nil
}

// ValidRegistry validate whether a pod has identified/valid docker registry
func (a *Agent) ValidRegistry(pod corev1.PodSpec) error {
	for _, container := range pod.Containers {
		var valid bool
		for _, registry := range a.cfg.ImageRegistries {
			if strings.Contains(container.Image, registry) {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("container %s used unidentified registry %s", container.Name, container.Image)
		}
	}
	return nil
}

// ValidProbe ...
func (a *Agent) ValidProbe(pod corev1.PodSpec) error {

	for _, container := range pod.Containers {
		if container.LivenessProbe == nil {
			return fmt.Errorf("container %s has no liveness probe configured", container.Name)
		}
		if container.LivenessProbe.TCPSocket == nil &&
			container.LivenessProbe.Exec == nil &&
			container.LivenessProbe.HTTPGet == nil {
			return fmt.Errorf("none of tcp socket, exec, and httpGet is configured for liveness probe in container %s", container.Name)
		}

		if container.ReadinessProbe == nil {
			return fmt.Errorf("container %s has no readiness probe configured", container.Name)
		}
		if container.ReadinessProbe.TCPSocket == nil &&
			container.ReadinessProbe.Exec == nil &&
			container.ReadinessProbe.HTTPGet == nil {
			return fmt.Errorf("none of tcp socket, exec, and httpGet is configured for readiness probe in container %s", container.Name)
		}
	}
	return nil
}

// ShouldIgnore ignore this deployment from validation if the deployment has
// additional annotations
func ShouldIgnore(deployment appsv1.Deployment) (bool, error) {
	raw, ok := deployment.Annotations[agent.AnnotationIgnoreCheck]
	if !ok {
		return false, nil
	}

	inject, err := strconv.ParseBool(raw)
	if err != nil {
		return false, err
	}

	return inject, nil
}
