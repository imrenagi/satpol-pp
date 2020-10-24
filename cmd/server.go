package cmd

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/hashicorp/vault-k8s/helper/cert"
	"github.com/imrenagi/satpol-pp/server"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	autoName     string
	autoHosts    string
	certFilePath string
	keyFilePath  string
	certStorage  atomic.Value
)

// NewServerCmd returns a new `version` command to be used as a sub-command to root
func NewServerCmd() *cobra.Command {

	serverCmd := cobra.Command{
		Use:   "server",
		Short: fmt.Sprintf("run server"),
		Run: func(cmd *cobra.Command, args []string) {

			ctx, cancelFunc := context.WithCancel(context.Background())
			defer cancelFunc()

			var config *rest.Config
			var err error

			if os.Getenv("ENV") == "development" {
				kubeconfig := filepath.Join(homeDir(), ".kube", "config")
				config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
				if err != nil {
					log.Fatal().Err(err).Msg("unable to create config from kubeconfig")
				}
			} else {
				config, err = rest.InClusterConfig()
				if err != nil {
					log.Fatal().Err(err).Msg("unable to create config from cluster service account")
				}
			}

			// creates the clientset
			clientset, err := kubernetes.NewForConfig(config)
			if err != nil {
				panic(err.Error())
			}

			// Determine where to source the certificates from
			var certSource cert.Source = &cert.GenSource{
				Name:  "Satpol PP",
				Hosts: strings.Split(autoHosts, ","),
			}
			if certFilePath != "" {
				certSource = &cert.DiskSource{
					CertPath: certFilePath,
					KeyPath:  keyFilePath,
				}
			}

			certCh := make(chan cert.Bundle)
			certNotify := cert.NewNotify(ctx, certCh, certSource)
			go certNotify.Run()
			go certWatcher(ctx, certCh, clientset)

			handler := server.Handler{
				Clientset: clientset,
				Log:       log.With().Timestamp().Logger(),
			}

			mux := http.NewServeMux()

			mux.HandleFunc("/", home)
			mux.HandleFunc("/pods/check", handler.PodCheckHandler())
			// mux.HandleFunc("/deployments/check", handler.Handle)
			mux.HandleFunc("/configmaps/check", handler.ConfigMapCheckHandler())

			// trusted docker registry
			// liveness and readiness probe
			// enough replica to serve traffic
			// no secret or sensitive information stored in configmap

			s := &http.Server{
				Addr:         ":8080",
				Handler:      mux,
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 10 * time.Second,
				TLSConfig:    &tls.Config{GetCertificate: getCertificate},
			}

			log.Warn().Msg("starting handler")

			go func() {
				log.Warn().Msgf("listening on %s", s.Addr)
				if err := s.ListenAndServeTLS(certFilePath, keyFilePath); err != nil {
					log.Fatal().Err(err).Msg("cant start server")
				}
			}()

			termChan := make(chan os.Signal)
			signal.Notify(termChan, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
			defer func() {
				signal.Stop(termChan)
				cancelFunc()
			}()

			select {
			case <-termChan:
				if err := s.Shutdown(ctx); err != nil {
					log.Fatal().Err(err).Msg("error shutting down handler")
				}
				cancelFunc()
			case <-ctx.Done():
			}
		},
	}

	serverCmd.Flags().StringVar(&autoName, "auto-name", os.Getenv("SATPOLPP_AUTO_NAME"), "name of mutation admission hook resource")
	serverCmd.Flags().StringVar(&autoHosts, "auto-hosts", os.Getenv("SATPOLPP_AUTO_HOST"), "all hosts name used for tls cert generation")
	serverCmd.Flags().StringVar(&certFilePath, "tls-cert", os.Getenv("SATPOLPP_CERT_FILE_PATH"), "tls certificate path")
	serverCmd.Flags().StringVar(&keyFilePath, "tls-key", os.Getenv("SATPOLPP_KEY_FILE_PATH"), "tls private key path")

	return &serverCmd
}

func getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	certRaw := certStorage.Load()
	if certRaw == nil {
		return nil, fmt.Errorf("no certificate available")
	}
	return certRaw.(*tls.Certificate), nil
}

func certWatcher(ctx context.Context, ch <-chan cert.Bundle, clientset *kubernetes.Clientset) {
	var bundle cert.Bundle
	for {
		select {
		case bundle = <-ch:
			log.Info().Msg("Updated certificate bundle received. Updating certs...")
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return
		}

		crt, err := tls.X509KeyPair(bundle.Cert, bundle.Key)
		if err != nil {
			log.Error().Err(err).Msg("Error loading TLS keypair")
			continue
		}

		if autoHosts != "" && len(bundle.CACert) > 0 {
			value := base64.StdEncoding.EncodeToString(bundle.CACert)
			_, err := clientset.AdmissionregistrationV1beta1().
				MutatingWebhookConfigurations().
				Patch(autoName, types.JSONPatchType, []byte(fmt.Sprintf(
					`[{
						"op": "add",
						"path": "/webhooks/0/clientConfig/caBundle",
						"value": %q
					}]`, value)))
			if err != nil {
				log.Error().Err(err).Msg("Error updating MutatingWebhookConfiguration")
				continue
			}
		}
		certStorage.Store(&crt)
	}
}

func home(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("healthy"))
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
