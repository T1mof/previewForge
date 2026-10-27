package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
	"github.com/T1mof/previewForge/internal/prenvironment"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "apply" && args[0] != "delete" && args[0] != "get" {
		return errors.New("usage: previewctl apply|delete|get --kubeconfig FILE --context CONTEXT --source-uid UID --repository-id ID --pr-number N [options]")
	}
	operation := args[0]
	flags := flag.NewFlagSet(operation, flag.ContinueOnError)
	var path, contextName, sourceName, sourceUID string
	var request prenvironment.Request
	var port, replicas int
	var wait bool
	var timeout time.Duration
	flags.StringVar(&path, "kubeconfig", "", "Explicit kubeconfig path (required)")
	flags.StringVar(&contextName, "context", "", "Explicit kubeconfig context (required)")
	flags.StringVar(&sourceName, "source-namespace", prenvironment.DefaultSourceNamespace, "Source Namespace installed by the administrator")
	flags.StringVar(&sourceUID, "source-uid", "", "Expected source Namespace UID (required)")
	flags.Int64Var(&request.RepositoryID, "repository-id", 0, "Stable numeric GitHub repository ID")
	flags.Int64Var(&request.Number, "pr-number", 0, "Pull request number")
	flags.StringVar(&request.Image, "image", "", "Immutable image reference including @sha256 digest")
	flags.StringVar(&request.Revision, "revision", "", "Full source commit SHA")
	flags.IntVar(&port, "port", 8080, "Application port")
	flags.IntVar(&replicas, "replicas", 1, "Application replicas")
	flags.Int64Var(&request.TTLSeconds, "ttl-seconds", 3600, "TTL from environment creation; updates do not reset it")
	flags.StringVar(&request.Host, "host", "", "Exact Ingress host; empty keeps the application internal")
	flags.StringVar(&request.ClassName, "ingress-class", "", "Explicit IngressClass, required with host")
	flags.StringVar(&request.TLSSecretName, "tls-secret", "", "Separately provisioned TLS Secret in the preview Namespace")
	flags.BoolVar(&wait, "wait", false, "Wait for the current rollout or completion of deletion")
	flags.DurationVar(&timeout, "timeout", 5*time.Minute, "Overall operation timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || path == "" || contextName == "" || sourceUID == "" {
		return errors.New("explicit kubeconfig, context and source UID are required; positional arguments are not supported")
	}
	if _, err := prenvironment.Name(request.RepositoryID, request.Number); err != nil {
		return err
	}
	if timeout <= 0 || port < 1 || port > 65535 || replicas < 1 || replicas > 10 {
		return errors.New("invalid timeout, port or replica count")
	}
	request.Port, request.Replicas = int32(port), int32(replicas)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("load explicit kubeconfig: %w", err)
	}
	if _, exists := raw.Contexts[contextName]; !exists {
		return errors.New("requested context does not exist in the supplied kubeconfig")
	}
	cfg, err := clientcmd.NewNonInteractiveClientConfig(*raw, contextName, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return fmt.Errorf("load explicit context: %w", err)
	}
	cfg.Timeout = 15 * time.Second
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := platformv1.AddToScheme(scheme); err != nil {
		return err
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	environments := &prenvironment.Environments{Client: c, Source: prenvironment.Binding{Namespace: sourceName, UID: types.UID(sourceUID)}}
	if operation == "delete" {
		uid, err := environments.Delete(ctx, request.RepositoryID, request.Number)
		if err != nil {
			return err
		}
		if wait && uid != "" {
			if err := poll(ctx, func() (bool, error) { return environments.Deleted(ctx, request.RepositoryID, request.Number, uid) }); err != nil {
				return err
			}
		}
		name, _ := prenvironment.Name(request.RepositoryID, request.Number)
		return json.NewEncoder(output).Encode(map[string]any{"name": name, "uid": uid, "deletionRequested": true, "waited": wait})
	}
	var p *platformv1.PreviewEnvironment
	if operation == "get" {
		p, err = environments.Get(ctx, request.RepositoryID, request.Number)
	} else {
		err = poll(ctx, func() (bool, error) {
			p, err = environments.Apply(ctx, request)
			if errors.Is(err, prenvironment.ErrTerminating) && wait {
				return false, nil
			}
			return err == nil, err
		})
	}
	if err != nil {
		return err
	}
	if wait {
		uid, generation, revision := p.UID, p.Generation, p.Annotations[prenvironment.RevisionAnnotation]
		err = poll(ctx, func() (bool, error) {
			current, err := environments.Get(ctx, request.RepositoryID, request.Number)
			if err != nil {
				return false, err
			}
			if current.UID != uid || current.Generation != generation || current.Annotations[prenvironment.RevisionAnnotation] != revision {
				return false, errors.New("environment was superseded during rollout; refusing a stale result")
			}
			p = current
			return prenvironment.Ready(p), nil
		})
		if err != nil {
			return fmt.Errorf("wait for current rollout: %w", err)
		}
	}
	return json.NewEncoder(output).Encode(map[string]any{"name": p.Name, "uid": p.UID, "namespace": p.Status.Namespace, "url": p.Status.URL, "phase": p.Status.Phase, "generation": p.Generation, "observedGeneration": p.Status.ObservedGeneration, "revision": p.Annotations[prenvironment.RevisionAnnotation]})
}

func poll(ctx context.Context, check func() (bool, error)) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		ready, err := check()
		if err != nil || ready {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
