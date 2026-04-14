package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strconv"
	"strings"
	"time"

	sdk "github.com/bubustack/bubu-sdk-go"
	sdkengram "github.com/bubustack/bubu-sdk-go/engram"
	sdkk8s "github.com/bubustack/bubu-sdk-go/k8s"
	"github.com/bubustack/bubu-sdk-go/runtime"
	"github.com/bubustack/core/contracts"
	"github.com/bubustack/core/runtime/identity"
	"github.com/bubustack/core/runtime/naming"

	cfgpkg "github.com/bubustack/mcp-adapter-engram/pkg/config"
	"github.com/bubustack/mcp-adapter-engram/pkg/kube"
	"github.com/bubustack/mcp-adapter-engram/pkg/mcp"
)

// This package implements the Engram that adapts BubuStack Story steps to an MCP server.
// Two mutually exclusive transports are supported:
//   - streamable_http: tiny client to an external MCP server (no K8s resources)
//   - stdio: one runner Pod per Engram instance; attach via Pods/Attach and speak JSON-RPC
//
// The Engram returns verbatim MCP payloads under output.data.result with light metadata in output.data.meta.

// Inputs captures per-call parameters across batch and streaming modes.
type Inputs struct {
	Action      string         `mapstructure:"action"`
	Tool        string         `mapstructure:"tool"`
	Arguments   map[string]any `mapstructure:"arguments"`
	Pagination  *Pagination    `mapstructure:"pagination"`
	ResourceURI string         `mapstructure:"resourceURI"`
	PromptName  string         `mapstructure:"promptName"`
	Reconcile   bool           `mapstructure:"reconcile"`
	Timeout     time.Duration  `mapstructure:"timeout"`
	Server      string         `mapstructure:"server"`
}

const defaultSecretBucket = "server"

type engramImpl struct {
	cfg       cfgpkg.Config
	secrets   *sdkengram.Secrets
	client    mcp.Client
	cleanup   func(context.Context) error
	runnerPod string
	streaming bool
	execData  *runtime.ExecutionContextData
}

func New() *engramImpl { return &engramImpl{} }

// Init wires the chosen transport.
func (e *engramImpl) Init(ctx context.Context, cfg cfgpkg.Config, secrets *sdkengram.Secrets) error {
	e.cfg = cfg
	e.secrets = secrets
	e.captureExecutionContext(ctx)
	t := strings.ToLower(e.cfg.Transport)
	if t == "streamable_http" {
		return e.initHTTP(ctx)
	}
	if t == "stdio" {
		return e.initStdio(ctx)
	}
	return fmt.Errorf("unsupported transport: %s", e.cfg.Transport)
}

func (e *engramImpl) initHTTP(ctx context.Context) error {
	if strings.TrimSpace(e.cfg.Server.BaseURL) == "" {
		return fmt.Errorf("server.baseURL is required for transport=streamable_http")
	}
	secretHeaders, err := e.loadHeadersFromSecret()
	if err != nil {
		return err
	}
	headers := map[string]string{}
	maps.Copy(headers, e.cfg.Server.Headers)
	c, err := mcp.NewHTTPClient(e.cfg.Server.BaseURL, secretHeaders, headers)
	if err != nil {
		return err
	}
	e.client = c
	if _, err := e.client.Initialize(ctx, e.cfg.MCP.InitClientCapabilities); err != nil {
		return err
	}
	_ = e.client.NotifyInitialized(ctx)
	_, _ = e.client.ListTools(ctx)
	if e.cfg.MCP.PostInitDelay > 0 {
		time.Sleep(e.cfg.MCP.PostInitDelay)
	}
	e.cleanup = func(context.Context) error { return nil }
	return nil
}

// errInvalidAction keeps Process() error payloads aligned with prior releases.
var errInvalidAction = errors.New("invalid action")

func (e *engramImpl) initStdio(ctx context.Context) error {
	logger := sdk.LoggerFromContext(ctx)
	ns := e.resolveNamespace()
	podName := resolvePodName()
	labelOwner := firstNonEmpty(podName, e.stepRunID(), e.stepName(), "mcp-adapter")
	_ = kube.ReapOldOwnedRunnerPods(ctx, ns, labelOwner, 2*time.Hour)
	stepRun := firstNonEmpty(e.stepRunID(), e.stepName(), "unknown")
	baseName := strings.TrimSpace(podName)
	if baseName == "" {
		baseName = stepRun
	}
	e.runnerPod = naming.ComposeDNS1123WithSuffix(baseName, "stdio")
	if e.debugEnabled(ctx, logger) {
		logger.Debug("Resolved MCP runner metadata",
			slog.String("namespace", ns),
			slog.String("runnerPod", e.runnerPod),
			slog.String("labelOwner", labelOwner),
			slog.String("stepRun", stepRun),
		)
	}
	env := e.buildEnvForRunner()
	// Reuse the bound secret directly unless ephemeral copy is explicitly requested.
	existingSecret := ""
	if !e.cfg.Stdio.UseEphemeralSecret {
		existingSecret = resolveSecretName(defaultSecretBucket)
		if existingSecret != "" {
			// Avoid duplicating values when attaching the entire Secret via EnvFrom.
			env = map[string]string{}
		}
	}
	_, ephemeralSecretName, err := kube.EnsureRunnerPod(ctx, kube.RunnerPodSpec{
		Namespace:    ns,
		Name:         e.runnerPod,
		Image:        e.cfg.Stdio.Image,
		PullPolicy:   e.cfg.Stdio.ImagePullPolicy,
		Command:      e.cfg.Stdio.Command,
		Args:         e.cfg.Stdio.Args,
		Env:          env,
		Resources:    e.cfg.Stdio.Resources,
		Security:     e.cfg.Stdio.Security,
		NodeSelector: e.cfg.Stdio.NodeSelector,
		Tolerations:  e.cfg.Stdio.Tolerations,
		Labels:       mergeMaps(e.runnerLabels(labelOwner), e.cfg.Stdio.PodLabels),
		Annotations:  mergeMaps(e.runnerAnnotations(), e.cfg.Stdio.PodAnnotations),
		// Set owner reference only when the adapter Pod name is known.
		OwnerPodName:                  podName,
		OwnerPodUID:                   "",
		TerminationGracePeriodSeconds: e.cfg.Stdio.TerminationGracePeriodSeconds,
		UseEphemeralSecret:            e.cfg.Stdio.UseEphemeralSecret,
		ExistingEnvSecretName:         existingSecret,
	})
	if err != nil {
		return fmt.Errorf("ensure runner pod: %w", err)
	}
	if err := kube.WaitForPodRunning(ctx, ns, e.runnerPod); err != nil {
		return fmt.Errorf("runner pod not running: %w", err)
	}
	sc, err := mcp.NewStdioClient(ctx, mcp.StdioOptions{Namespace: ns, PodName: e.runnerPod, Container: "with-stdio"})
	if err != nil {
		return err
	}
	e.client = sc
	if _, err := e.client.Initialize(ctx, e.cfg.MCP.InitClientCapabilities); err != nil {
		return err
	}
	// Per MCP spec: send notifications/initialized after successful initialize handshake.
	_ = e.client.NotifyInitialized(ctx)
	// Probe readiness: ListTools forces a round-trip, giving the server time to
	// complete any post-init setup (e.g. auto-login for Discord/Slack MCP servers).
	_, _ = e.client.ListTools(ctx)
	if e.cfg.MCP.PostInitDelay > 0 {
		time.Sleep(e.cfg.MCP.PostInitDelay)
	}
	pol := strings.ToLower(e.cfg.Stdio.DeletionPolicy)
	e.cleanup = func(c context.Context) error {
		if pol == "keep" {
			return nil
		}
		return kube.DeleteRunnerPod(c, ns, e.runnerPod, ephemeralSecretName)
	}
	return nil
}

// Process executes a single MCP action and returns verbatim payloads under output.data.result.
func (e *engramImpl) Process(
	ctx context.Context, exec *sdkengram.ExecutionContext, in Inputs,
) (*sdkengram.Result, error) {
	logger := e.batchLogger(ctx, exec)
	e.logAdapterRequest(ctx, logger, in)
	start := time.Now()
	if e.client == nil {
		return sdkengram.NewResultFrom(map[string]any{
			"data": map[string]any{"error": map[string]any{"message": "client not initialized"}},
		}), nil
	}

	ctx, cancel := withOptionalTimeout(ctx, in.Timeout)
	defer cancel()

	data := e.executeAndNormalize(ctx, logger, in, start)
	meta := e.resultMeta(in, start)
	if len(meta) > 0 {
		data["meta"] = meta
	}

	// Best-effort cleanup for batch mode to honor deletionPolicy
	if !e.streaming && e.cleanup != nil {
		_ = e.cleanup(ctx)
	}

	e.logAdapterResponse(ctx, logger, data)
	return sdkengram.NewResultFrom(map[string]any{"data": data}), nil
}

func (e *engramImpl) executeAndNormalize(
	ctx context.Context,
	logger *slog.Logger,
	in Inputs,
	start time.Time,
) map[string]any {
	data := make(map[string]any)
	result, err := e.executeAction(ctx, in)
	if err != nil {
		recordActionError(data, err)
		if logger != nil {
			logger.Warn("MCP action failed", slog.String("action", in.Action), slog.String("error", dataErrorMessage(data)))
		}
		return data
	}
	if result != nil {
		if err := e.storeNormalizedResult(ctx, in, result, data); err != nil {
			data["error"] = map[string]any{"message": err.Error()}
			if logger != nil {
				logger.Warn("MCP pagination failed", slog.String("action", in.Action), slog.String("error", err.Error()))
			}
		}
	}
	if logger != nil {
		logger.Info("MCP action completed",
			slog.String("action", in.Action),
			slog.Int64("durationMs", time.Since(start).Milliseconds()),
		)
	}
	return data
}

func (e *engramImpl) storeNormalizedResult(
	ctx context.Context,
	in Inputs,
	result any,
	data map[string]any,
) error {
	normalized, parsed, err := e.normalizeAndPaginate(ctx, in, result)
	if err != nil {
		return err
	}
	data["result"] = normalized
	if parsed != nil {
		data["resultParsed"] = parsed
	}
	return nil
}

func recordActionError(data map[string]any, err error) {
	msg := err.Error()
	if errors.Is(err, errInvalidAction) {
		msg = errInvalidAction.Error()
	}
	data["error"] = map[string]any{"message": msg}
}

func dataErrorMessage(data map[string]any) string {
	errBlock, ok := data["error"].(map[string]any)
	if !ok {
		return ""
	}
	msg, _ := errBlock["message"].(string)
	return msg
}

func (e *engramImpl) resultMeta(in Inputs, start time.Time) map[string]any {
	meta := map[string]any{
		"durationMs": time.Since(start).Milliseconds(),
	}
	if in.Tool != "" {
		meta["tool"] = in.Tool
	}
	switch strings.ToLower(e.cfg.Transport) {
	case "streamable_http":
		if e.cfg.Server.BaseURL != "" {
			meta["server"] = e.cfg.Server.BaseURL
		}
	case "stdio":
		if e.runnerPod != "" {
			meta["server"] = e.runnerPod
		}
	}
	return meta
}

func (e *engramImpl) executeAction(ctx context.Context, in Inputs) (any, error) {
	switch in.Action {
	case "reconcile":
		// No-op for now; kube ensure handled elsewhere
		return map[string]any{"ok": true}, nil
	case "listTools":
		return e.client.ListTools(ctx)
	case "callTool":
		return e.callToolWithRetry(ctx, in)
	case "listResources":
		return e.client.ListResources(ctx)
	case "readResource":
		return e.client.ReadResource(ctx, in.ResourceURI)
	case "getPrompt":
		return e.client.GetPrompt(ctx, in.PromptName)
	default:
		return nil, fmt.Errorf("%w: %s", errInvalidAction, in.Action)
	}
}

// callToolWithRetry wraps CallTool with retry logic for MCP servers that return
// isError:true during async post-init setup (e.g. Discord "not logged in").
func (e *engramImpl) callToolWithRetry(ctx context.Context, in Inputs) (any, error) {
	result, err := e.client.CallTool(ctx, in.Tool, in.Arguments)
	if err != nil {
		return result, err
	}

	retries := e.cfg.MCP.CallToolRetries
	if retries <= 0 {
		return result, nil
	}

	delay := e.cfg.MCP.CallToolRetryDelay
	if delay <= 0 {
		delay = 2 * time.Second
	}

	for attempt := 0; attempt < retries; attempt++ {
		if !isMCPTransientError(result) {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(delay):
		}
		result, err = e.client.CallTool(ctx, in.Tool, in.Arguments)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// isMCPTransientError classifies MCP application-level errors that are likely
// transient and worth retrying. Permanent tool errors should not be retried.
func isMCPTransientError(result any) bool {
	payload, ok := unwrapMCPResult(result)
	if !ok {
		return false
	}
	isErr, _ := payload["isError"].(bool)
	if !isErr {
		return false
	}
	if code, ok := extractMCPErrorCode(payload); ok && isTransientErrorCode(code) {
		return true
	}
	for _, msg := range extractMCPErrorMessages(payload) {
		if isTransientErrorMessage(msg) {
			return true
		}
	}
	return false
}

// Stream processes streaming messages using the already-initialized MCP client.
func (e *engramImpl) Stream(
	ctx context.Context,
	in <-chan sdkengram.InboundMessage,
	out chan<- sdkengram.StreamMessage,
) error {
	logger := e.streamLogger(ctx)

	// client is created and initialized during Init
	e.streaming = true
	defer func() {
		e.streaming = false
		if e.cleanup != nil {
			_ = e.cleanup(ctx)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-in:
			if !ok {
				return nil
			}

			inputs, skip, err := decodeStreamInputs(msg)
			if err != nil {
				logger.Warn("Failed to decode stream inputs", "error", err)
				msg.Done()
				continue
			}
			if skip {
				logger.Warn("Stream message missing payload and inputs", "metadata", msg.Metadata)
				msg.Done()
				continue
			}

			res, err := e.Process(ctx, nil, inputs)
			if err != nil {
				return err
			}

			payload, err := marshalStreamResult(res)
			if err != nil {
				return err
			}

			response := newStreamResponse(msg, payload)

			select {
			case out <- response:
				msg.Done()
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// buildEnvForRunner flattens only the 'server' secret bucket into env for the stdio runner.
// Exposing additional buckets to runner Pods can leak unrelated credentials.
func (e *engramImpl) buildEnvForRunner() map[string]string {
	return e.expandSecretBucket(defaultSecretBucket)
}

func (e *engramImpl) loadHeadersFromSecret() (map[string]string, error) {
	out := map[string]string{}
	if len(e.cfg.Server.HeadersFromSecret) == 0 {
		return out, nil
	}
	for header, ref := range e.cfg.Server.HeadersFromSecret {
		bucket, key := parseSecretRef(ref)
		if key == "" {
			return nil, fmt.Errorf("server.headersFromSecret[%s]: secret reference %q must include a key", header, ref)
		}
		values := e.expandSecretBucket(bucket)
		val, ok := values[key]
		if !ok {
			return nil, fmt.Errorf("server.headersFromSecret[%s]: key %q not found in secret bucket %q", header, key, bucket)
		}
		out[header] = val
	}
	return out, nil
}

func withOptionalTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d > 0 {
		return context.WithTimeout(ctx, d)
	}
	return context.WithCancel(ctx)
}

func (e *engramImpl) expandSecretBucket(bucket string) map[string]string {
	out := map[string]string{}
	if bucket == "" {
		return out
	}
	desc := resolveSecretDescriptor(bucket)
	if prefix, ok := strings.CutPrefix(desc, "env:"); ok {
		for _, env := range os.Environ() {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) != 2 {
				continue
			}
			name, value := parts[0], parts[1]
			if key, ok := strings.CutPrefix(name, prefix); ok {
				if key != "" {
					out[key] = value
				}
			}
		}
		return out
	}
	if dir, ok := strings.CutPrefix(desc, "file:"); ok {
		entries, _ := os.ReadDir(dir)
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			b, err := os.ReadFile(dir + "/" + ent.Name())
			if err == nil {
				out[ent.Name()] = strings.TrimRight(string(b), "\n")
			}
		}
	}
	return out
}

func mergeMaps(a, b map[string]string) map[string]string {
	out := maps.Clone(a)
	maps.Copy(out, b)
	return out
}

func parseSecretRef(ref string) (string, string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return defaultSecretBucket, ""
	}
	if bucket, key, ok := strings.Cut(ref, ":"); ok {
		bucket = strings.TrimSpace(bucket)
		key = strings.TrimSpace(key)
		if bucket == "" {
			bucket = defaultSecretBucket
		}
		return bucket, key
	}
	return defaultSecretBucket, ref
}

func decodeStreamInputs(msg sdkengram.InboundMessage) (Inputs, bool, error) {
	var inputs Inputs

	raw := streamJSONBytes(msg)
	if len(raw) == 0 {
		return Inputs{}, true, nil
	}
	if err := json.Unmarshal(raw, &inputs); err != nil {
		return Inputs{}, false, fmt.Errorf("unmarshal stream inputs: %w", err)
	}
	return inputs, false, nil
}

func streamJSONBytes(msg sdkengram.InboundMessage) []byte {
	if len(msg.Inputs) > 0 {
		return msg.Inputs
	}
	if len(msg.Payload) > 0 {
		return msg.Payload
	}
	if msg.Binary != nil && len(msg.Binary.Payload) > 0 {
		return msg.Binary.Payload
	}
	return nil
}

func marshalStreamResult(res *sdkengram.Result) ([]byte, error) {
	if res == nil || res.Data == nil {
		return nil, nil
	}
	payload, err := json.Marshal(res.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal stream response: %w", err)
	}
	return payload, nil
}

func newStreamResponse(msg sdkengram.InboundMessage, payload []byte) sdkengram.StreamMessage {
	body := append([]byte(nil), payload...)
	return sdkengram.StreamMessage{
		Metadata: cloneMetadata(msg.Metadata),
		Inputs:   append([]byte(nil), body...),
		Payload:  body,
		Binary: &sdkengram.BinaryFrame{
			Payload:  append([]byte(nil), body...),
			MimeType: "application/json",
		},
	}
}

func cloneMetadata(meta map[string]string) map[string]string {
	if len(meta) == 0 {
		return nil
	}
	cp := make(map[string]string, len(meta))
	for k, v := range meta {
		cp[k] = v
	}
	return cp
}

func (e *engramImpl) captureExecutionContext(ctx context.Context) {
	data, err := runtime.LoadExecutionContextData()
	if err != nil {
		if logger := sdk.LoggerFromContext(ctx); logger != nil {
			if e.debugEnabled(ctx, logger) {
				logger.Debug("Execution context unavailable for MCP adapter", slog.Any("error", err))
			}
		}
		return
	}
	e.execData = data
}

func (e *engramImpl) storyInfo() sdkengram.StoryInfo {
	if e.execData != nil {
		return e.execData.StoryInfo
	}
	return sdkengram.StoryInfo{}
}

func (e *engramImpl) stepRunID() string {
	return strings.TrimSpace(e.storyInfo().StepRunID)
}

func (e *engramImpl) stepName() string {
	return strings.TrimSpace(e.storyInfo().StepName)
}

func (e *engramImpl) resolveNamespace() string {
	if ns := strings.TrimSpace(e.storyInfo().StepRunNamespace); ns != "" {
		return ns
	}
	return sdkk8s.ResolvePodNamespace()
}

func (e *engramImpl) engramName() string {
	return strings.TrimSpace(os.Getenv(contracts.EngramNameEnv))
}

func (e *engramImpl) runnerLabels(owner string) map[string]string {
	labels := map[string]string{
		"bubustack.io/ownerPod":  owner,
		"app.kubernetes.io/name": "mcp-adapter-engram",
	}
	if storyRun := strings.TrimSpace(e.storyInfo().StoryRunID); storyRun != "" {
		labels[contracts.StoryRunLabelKey] = identity.LabelValueFromName(storyRun)
	}
	if stepRun := strings.TrimSpace(e.storyInfo().StepRunID); stepRun != "" {
		labels[contracts.StepRunLabelKey] = identity.LabelValueFromName(stepRun)
	}
	if step := strings.TrimSpace(e.storyInfo().StepName); step != "" {
		labels[contracts.StepLabelKey] = identity.LabelValueFromName(step)
	}
	if eng := e.engramName(); eng != "" {
		labels[contracts.EngramLabelKey] = identity.LabelValueFromName(eng)
	}
	return labels
}

func (e *engramImpl) runnerAnnotations() map[string]string {
	ann := map[string]string{}
	if storyRun := strings.TrimSpace(e.storyInfo().StoryRunID); storyRun != "" {
		ann[contracts.StoryRunAnnotation] = storyRun
	}
	if step := strings.TrimSpace(e.storyInfo().StepName); step != "" {
		ann[contracts.StepAnnotation] = step
	}
	if stepRun := strings.TrimSpace(e.storyInfo().StepRunID); stepRun != "" {
		ann[contracts.StepRunLabelKey] = stepRun
	}
	return ann
}

func resolveSecretName(bucket string) string {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		bucket = defaultSecretBucket
	}
	return strings.TrimSpace(os.Getenv(fmt.Sprintf("%s%s_NAME", contracts.SecretPrefixEnv, bucket)))
}

func resolveSecretDescriptor(bucket string) string {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		bucket = defaultSecretBucket
	}
	return strings.TrimSpace(os.Getenv(contracts.SecretPrefixEnv + bucket))
}

func unwrapMCPResult(result any) (map[string]any, bool) {
	m, ok := result.(map[string]any)
	if !ok {
		return nil, false
	}
	if inner, ok := m["result"]; ok {
		if innerMap, ok := inner.(map[string]any); ok {
			return innerMap, true
		}
	}
	return m, true
}

func extractMCPErrorCode(payload map[string]any) (int, bool) {
	if code, ok := parseNumericCode(payload["code"]); ok {
		return code, true
	}
	if errObj, ok := payload["error"].(map[string]any); ok {
		return parseNumericCode(errObj["code"])
	}
	return 0, false
}

func parseNumericCode(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func isTransientErrorCode(code int) bool {
	if code >= -32099 && code <= -32000 {
		return true
	}
	switch code {
	case 408, 425, 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

func extractMCPErrorMessages(payload map[string]any) []string {
	msgs := []string{}
	if msg, ok := payload["message"].(string); ok && strings.TrimSpace(msg) != "" {
		msgs = append(msgs, msg)
	}
	if errObj, ok := payload["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
			msgs = append(msgs, msg)
		}
	}
	if content, ok := payload["content"].([]any); ok {
		for _, item := range content {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := entry["text"].(string); ok && strings.TrimSpace(text) != "" {
				msgs = append(msgs, text)
			}
		}
	}
	return msgs
}

func isTransientErrorMessage(message string) bool {
	msg := strings.ToLower(strings.TrimSpace(message))
	if msg == "" {
		return false
	}
	transientHints := []string{
		"temporar",
		"try again",
		"timeout",
		"timed out",
		"deadline exceeded",
		"not logged in",
		"rate limit",
		"too many requests",
		"connection reset",
		"connection refused",
		"upstream",
		"unavailable",
		"service busy",
		"eai_again",
	}
	for _, hint := range transientHints {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func resolvePodName() string {
	if name := strings.TrimSpace(os.Getenv(contracts.PodNameEnv)); name != "" {
		return name
	}
	return strings.TrimSpace(os.Getenv("BUBU_POD_NAME"))
}

func (e *engramImpl) batchLogger(ctx context.Context, exec *sdkengram.ExecutionContext) *slog.Logger {
	if exec != nil && exec.Logger() != nil {
		return exec.Logger().With("component", "mcp-adapter-engram", "mode", "batch")
	}
	if logger := sdk.LoggerFromContext(ctx); logger != nil {
		return logger.With("component", "mcp-adapter-engram", "mode", "batch")
	}
	return slog.Default().With("component", "mcp-adapter-engram", "mode", "batch")
}

func (e *engramImpl) streamLogger(ctx context.Context) *slog.Logger {
	if logger := sdk.LoggerFromContext(ctx); logger != nil {
		return logger.With("component", "mcp-adapter-engram", "mode", "stream")
	}
	return slog.Default().With("component", "mcp-adapter-engram", "mode", "stream")
}
