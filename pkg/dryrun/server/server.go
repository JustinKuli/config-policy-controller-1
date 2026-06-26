// Copyright Contributors to the Open Cluster Management project

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/stolostron/go-template-utils/v7/pkg/lint"
	"open-cluster-management.io/config-policy-controller/pkg/dryrun"
)

// NewCommand returns a cobra command that runs the dryrun web server. Register it
// as `dryrun serve` from cmd/dryrun/main.go.
func NewCommand(cfg *Config) *cobra.Command {
	if cfg == nil {
		cfg = &Config{}
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the ConfigurationPolicy dryrun web server",
		Long: "Start an HTTP server that exposes the dryrun evaluation API and, " +
			"when built with embedded UI assets, serves the web interface.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Run(cmd.Context(), *cfg)
		},
	}

	cmd.Flags().StringVar(&cfg.Addr, "addr", ":8080", "Address and port to listen on")
	cmd.Flags().BoolVar(&cfg.APIOnly, "api-only", false,
		"Serve the evaluation API only, without the web UI")

	return cmd
}

const maxEvaluateBodyBytes = 5 << 20

// Config holds settings for the dryrun HTTP server.
type Config struct {
	// Addr is the listen address, for example ":8080".
	Addr string
	// StaticFS, when set, serves the web UI from an fs.FS (for example the
	// embedded assets from webui.Dist()).
	StaticFS fs.FS
	// APIOnly disables the web UI even when StaticFS is set.
	APIOnly bool
}

// Run starts the HTTP server and blocks until ctx is cancelled or the server fails.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}

	handler := NewHandler(cfg)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)

	go func() {
		log.Printf("dryrun web server listening on %s", cfg.Addr)

		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}

		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// NewHandler returns the HTTP handler for the dryrun API and optional static UI.
func NewHandler(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/evaluate", handleEvaluate)
	mux.HandleFunc("POST /api/lint", handleLint)

	if cfg.StaticFS != nil && !cfg.APIOnly {
		mux.Handle("/", newStaticHandler(cfg.StaticFS))
	}

	return mux
}

func newStaticHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fileServer.ServeHTTP(w, r)
	})
}

type evaluateRequest struct {
	Policy    string `json:"policy"`
	Resources string `json:"resources"`
}

type evaluateErrorResponse struct {
	Error string `json:"error"`
}

func handleEvaluate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	body, err := readEvaluateBody(w, r)
	if err != nil {
		writeEvaluateError(w, http.StatusBadRequest, err)

		return
	}

	var req evaluateRequest

	if err := json.Unmarshal(body, &req); err != nil {
		writeEvaluateError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON request body: %w", err))

		return
	}

	result, err := dryrun.Evaluate(r.Context(), dryrun.EvaluateInput{
		PolicyYAML:    req.Policy,
		ResourcesYAML: req.Resources,
	})
	if err != nil && !errors.Is(err, dryrun.ErrNonCompliant) {
		writeEvaluateError(w, evaluateErrorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}

type lintRequest struct {
	Policy string `json:"policy"`
}

type lintIssue struct {
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	RuleID   string `json:"ruleId"`
	Source   string `json:"source"`
}

type lintResponse struct {
	Issues []lintIssue `json:"issues"`
}

func handleLint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	body, err := readEvaluateBody(w, r)
	if err != nil {
		writeEvaluateError(w, http.StatusBadRequest, err)

		return
	}

	var req lintRequest

	if err := json.Unmarshal(body, &req); err != nil {
		writeEvaluateError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON request body: %w", err))

		return
	}

	violations := lint.Lint(req.Policy)
	writeJSON(w, http.StatusOK, lintResponse{Issues: violationsToIssues(violations)})
}

func violationsToIssues(violations []lint.LinterRuleViolation) []lintIssue {
	if len(violations) == 0 {
		return []lintIssue{}
	}

	issues := make([]lintIssue, 0, len(violations))

	for _, violation := range violations {
		severity := "warning"

		if metadata := lint.GetRuleMetadata(violation.RuleID); metadata != nil {
			severity = metadata.Level
		}

		column := violation.Column
		if column == 0 {
			column = 1
		}

		issues = append(issues, lintIssue{
			Line:     violation.LineNumber,
			Column:   column,
			Severity: severity,
			Message:  violation.Message,
			RuleID:   violation.RuleID,
			Source:   "Policy",
		})
	}

	return issues
}

func readEvaluateBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	defer r.Body.Close()

	limited := http.MaxBytesReader(w, r.Body, maxEvaluateBodyBytes)

	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("could not read request body: %w", err)
	}

	return body, nil
}

func evaluateErrorStatus(err error) int {
	msg := err.Error()

	if strings.Contains(msg, "unable to read input policy") ||
		strings.Contains(msg, "unable to read input resources") ||
		strings.Contains(msg, "unable to apply input resources") {
		return http.StatusBadRequest
	}

	return http.StatusInternalServerError
}

func writeEvaluateError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, evaluateErrorResponse{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.WriteHeader(status)

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(payload); err != nil {
		log.Printf("failed to encode JSON response: %v", err)
	}
}
