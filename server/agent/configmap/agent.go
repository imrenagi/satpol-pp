package configmap

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	dlp "cloud.google.com/go/dlp/apiv2"
	"github.com/imrenagi/satpol-pp/server/agent"
	"github.com/rs/zerolog/log"
	dlppb "google.golang.org/genproto/googleapis/privacy/dlp/v2"
	corev1 "k8s.io/api/core/v1"
)

type AgentConfig struct {
	GoogleProjectID string
}

type Agent struct {
	dlpclient *dlp.Client
	cfg       *AgentConfig
}

// New ...
func New(cfg *AgentConfig) (*Agent, error) {

	if cfg == nil {
		return nil, fmt.Errorf("agent config cant be nil")
	}

	ctx := context.Background()
	dlpclient, err := dlp.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	log.Debug().Msg("dlpclient is created")

	agent := &Agent{
		dlpclient: dlpclient,
		cfg:       cfg,
	}
	return agent, nil
}

// ShouldCheck ...
func ShouldCheck(configmap corev1.ConfigMap) (bool, error) {
	raw, ok := configmap.Annotations[agent.AnnotationShouldCheck]
	if !ok {
		return false, nil
	}

	check, err := strconv.ParseBool(raw)
	if err != nil {
		return false, err
	}

	return check, nil
}

// Validate ...
func (a *Agent) Validate(configmap corev1.ConfigMap) error {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var b strings.Builder
	for k, v := range configmap.Data {
		fmt.Fprintf(&b, "%s: %s\n", k, v)
	}
	textToInspect := b.String()

	log.Debug().Str("text", textToInspect).Msg("text to inspect is constructed")

	// Create and send the request.
	req := &dlppb.InspectContentRequest{
		Parent: fmt.Sprintf("projects/%s/locations/global", a.cfg.GoogleProjectID),
		Item: &dlppb.ContentItem{
			DataItem: &dlppb.ContentItem_Value{
				Value: textToInspect,
			},
		},
		InspectConfig: &dlppb.InspectConfig{
			InfoTypes: []*dlppb.InfoType{
				{Name: "AUTH_TOKEN"},
				{Name: "AWS_CREDENTIALS"},
				{Name: "BASIC_AUTH_HEADER"},
				{Name: "GCP_CREDENTIALS"},
				{Name: "GCP_API_KEY"},
				{Name: "JSON_WEB_TOKEN"},
				{Name: "PASSWORD"},
				{Name: "WEAK_PASSWORD_HASH"},
				{Name: "ENCRYPTION_KEY"},
			},
			IncludeQuote: true,
		},
	}
	resp, err := a.dlpclient.InspectContent(ctx, req)
	if err != nil {
		return err
	}

	log.Debug().Msg("dlp inspection is completed")

	var msgb strings.Builder
	result := resp.Result
	for _, f := range result.Findings {
		log.Debug().
			Str("quote", f.Quote).
			Str("info_type", f.InfoType.Name).
			Str("likelihood", f.Likelihood.String()).
			Msg("possible detection")

		if f.Likelihood >= dlppb.Likelihood_POSSIBLE {
			fmt.Fprintf(&msgb, "\n%s -> detected as %s (%s)", censor(f.Quote), f.InfoType.Name, f.Likelihood.String())
		}
	}

	msg := msgb.String()
	if msg != "" {
		return fmt.Errorf(msg)
	}

	return nil
}
