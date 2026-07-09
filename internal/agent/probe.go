package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const drawableLatencyCap = 5 * time.Second

func RunTCPProbe(ctx context.Context, target ProbeTarget) []ProbeSample {
	count := target.Count
	if count <= 0 {
		count = 1
	}
	timeout := normalizedProbeTimeout(target.TimeoutMS)
	observationTimeout := latencyObservationTimeout(timeout)
	if target.Port == nil {
		return failedProbeSamples(count, "missing_port")
	}

	address := net.JoinHostPort(target.Address, strconv.Itoa(*target.Port))
	samples := make([]ProbeSample, 0, count)
	for seq := 1; seq <= count; seq++ {
		select {
		case <-ctx.Done():
			for failedSeq := seq; failedSeq <= count; failedSeq++ {
				samples = append(samples, ProbeSample{Seq: failedSeq, Success: false, Error: "cancelled"})
			}
			return samples
		default:
		}
		dialCtx, cancel := context.WithTimeout(ctx, observationTimeout)
		start := time.Now()
		conn, err := (&net.Dialer{Timeout: observationTimeout}).DialContext(dialCtx, "tcp", address)
		elapsedMS := float64(time.Since(start).Microseconds()) / 1000
		cancel()
		if err != nil {
			samples = append(samples, failedMeasuredProbeSample(seq, elapsedMS, classifyProbeError(err)))
			continue
		}
		_ = conn.Close()
		samples = append(samples, measuredProbeSample(seq, elapsedMS, timeout))
	}
	return samples
}

var pingLatencyPattern = regexp.MustCompile(`time[=<]([0-9.]+)\s*ms`)

func RunPingProbe(ctx context.Context, target ProbeTarget) []ProbeSample {
	count := target.Count
	if count <= 0 {
		count = 1
	}
	timeout := normalizedProbeTimeout(target.TimeoutMS)
	observationTimeout := latencyObservationTimeout(timeout)
	address := strings.TrimSpace(target.Address)
	if address == "" || strings.HasPrefix(address, "-") {
		return failedProbeSamples(count, "invalid_address")
	}

	samples := make([]ProbeSample, 0, count)
	for seq := 1; seq <= count; seq++ {
		select {
		case <-ctx.Done():
			for failedSeq := seq; failedSeq <= count; failedSeq++ {
				samples = append(samples, ProbeSample{Seq: failedSeq, Success: false, Error: "cancelled"})
			}
			return samples
		default:
		}

		attemptCtx, cancel := context.WithTimeout(ctx, observationTimeout+time.Second)
		start := time.Now()
		cmd := exec.CommandContext(attemptCtx, "ping", "-n", "-c", "1", "-W", strconv.Itoa(pingTimeoutSeconds(observationTimeout)), address)
		output, err := cmd.CombinedOutput()
		elapsedMS := float64(time.Since(start).Microseconds()) / 1000
		ctxErr := attemptCtx.Err()
		cancel()
		if err != nil {
			samples = append(samples, failedMeasuredProbeSample(seq, elapsedMS, classifyPingError(err, string(output), ctxErr)))
			continue
		}
		latency := elapsedMS
		if parsed, ok := parsePingLatencyMS(string(output)); ok {
			latency = parsed
		}
		samples = append(samples, measuredProbeSample(seq, latency, timeout))
	}
	return samples
}

func RunHTTPProbe(ctx context.Context, target ProbeTarget) []ProbeSample {
	count := target.Count
	if count <= 0 {
		count = 1
	}
	timeout := normalizedProbeTimeout(target.TimeoutMS)
	observationTimeout := latencyObservationTimeout(timeout)
	address := strings.TrimSpace(target.Address)
	if !validHTTPProbeURL(address) {
		return failedProbeSamples(count, "invalid_url")
	}

	client := &http.Client{Timeout: observationTimeout}
	samples := make([]ProbeSample, 0, count)
	for seq := 1; seq <= count; seq++ {
		select {
		case <-ctx.Done():
			for failedSeq := seq; failedSeq <= count; failedSeq++ {
				samples = append(samples, ProbeSample{Seq: failedSeq, Success: false, Error: "cancelled"})
			}
			return samples
		default:
		}

		attemptCtx, cancel := context.WithTimeout(ctx, observationTimeout)
		request, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, address, nil)
		if err != nil {
			cancel()
			samples = append(samples, ProbeSample{Seq: seq, Success: false, Error: "invalid_url"})
			continue
		}
		request.Header.Set("User-Agent", "Zeno-Agent")
		start := time.Now()
		response, err := client.Do(request)
		elapsedMS := float64(time.Since(start).Microseconds()) / 1000
		ctxErr := attemptCtx.Err()
		cancel()
		if err != nil {
			samples = append(samples, failedMeasuredProbeSample(seq, elapsedMS, classifyHTTPProbeError(err, ctxErr)))
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 400 {
			samples = append(samples, ProbeSample{Seq: seq, Success: false, Error: fmt.Sprintf("http_status_%d", response.StatusCode)})
			continue
		}
		samples = append(samples, measuredProbeSample(seq, elapsedMS, timeout))
	}
	return samples
}

func normalizedProbeTimeout(timeoutMS int) time.Duration {
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout <= 0 {
		return time.Second
	}
	return timeout
}

func latencyObservationTimeout(timeout time.Duration) time.Duration {
	if timeout < drawableLatencyCap {
		return drawableLatencyCap
	}
	return timeout
}

func measuredProbeSample(seq int, elapsedMS float64, timeout time.Duration) ProbeSample {
	latency := cappedDrawableLatencyMS(elapsedMS)
	if time.Duration(elapsedMS*float64(time.Millisecond)) > timeout {
		return ProbeSample{Seq: seq, Success: false, LatencyMS: &latency, Error: "timeout"}
	}
	return ProbeSample{Seq: seq, Success: true, LatencyMS: &latency}
}

func failedMeasuredProbeSample(seq int, elapsedMS float64, errText string) ProbeSample {
	if errText != "timeout" {
		return ProbeSample{Seq: seq, Success: false, Error: errText}
	}
	latency := cappedDrawableLatencyMS(elapsedMS)
	return ProbeSample{Seq: seq, Success: false, LatencyMS: &latency, Error: errText}
}

func cappedDrawableLatencyMS(elapsedMS float64) float64 {
	if elapsedMS < 0 {
		return 0
	}
	capMS := float64(drawableLatencyCap / time.Millisecond)
	if elapsedMS > capMS {
		return capMS
	}
	return elapsedMS
}

func validHTTPProbeURL(address string) bool {
	parsed, err := url.ParseRequestURI(address)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func pingTimeoutSeconds(timeout time.Duration) int {
	seconds := int((timeout + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func parsePingLatencyMS(output string) (float64, bool) {
	matches := pingLatencyPattern.FindStringSubmatch(output)
	if len(matches) != 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

func classifyPingError(err error, output string, ctxErr error) string {
	if ctxErr != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	message := strings.ToLower(output + " " + err.Error())
	if strings.Contains(message, "executable file not found") || strings.Contains(message, "no such file") {
		return "ping_unavailable"
	}
	if strings.Contains(message, "name or service not known") || strings.Contains(message, "temporary failure") || strings.Contains(message, "unknown host") || strings.Contains(message, "no such host") {
		return "dns_error"
	}
	if strings.Contains(message, "operation not permitted") || strings.Contains(message, "permission denied") {
		return "permission_denied"
	}
	if strings.Contains(message, "100% packet loss") || strings.Contains(message, "0 received") || strings.Contains(message, "timeout") {
		return "timeout"
	}
	return "ping_error"
}

func classifyHTTPProbeError(err error, ctxErr error) string {
	if ctxErr != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "no such host") {
		return "dns_error"
	}
	if strings.Contains(message, "certificate") || strings.Contains(message, "tls") {
		return "tls_error"
	}
	if strings.Contains(message, "timeout") || strings.Contains(message, "deadline") || strings.Contains(message, "i/o timeout") {
		return "timeout"
	}
	return "http_error"
}

func failedProbeSamples(count int, errText string) []ProbeSample {
	if count <= 0 {
		return nil
	}
	samples := make([]ProbeSample, 0, count)
	for seq := 1; seq <= count; seq++ {
		samples = append(samples, ProbeSample{Seq: seq, Success: false, Error: errText})
	}
	return samples
}

func classifyProbeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return "timeout"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "timeout") || strings.Contains(message, "deadline") || strings.Contains(message, "i/o timeout") {
		return "timeout"
	}
	if strings.Contains(message, "no such host") {
		return "dns_error"
	}
	return "connect_error"
}

func ProbeTargets(ctx context.Context, targets []ProbeTarget, ts time.Time) []ProbeRound {
	rounds := make([]ProbeRound, len(targets))
	var wg sync.WaitGroup
	for index, target := range targets {
		index, target := index, target
		wg.Add(1)
		go func() {
			defer wg.Done()

			var samples []ProbeSample
			switch target.Type {
			case "tcping", "tcp":
				samples = RunTCPProbe(ctx, target)
			case "ping", "icmp":
				samples = RunPingProbe(ctx, target)
			case "http_get", "http", "https":
				samples = RunHTTPProbe(ctx, target)
			default:
				samples = failedProbeSamples(target.Count, fmt.Sprintf("unsupported_%s", target.Type))
			}
			rounds[index] = ProbeRound{TargetID: target.ID, TS: ts, Type: target.Type, Samples: samples}
		}()
	}
	wg.Wait()
	return rounds
}

func parseKeyValueLines(content string) map[string]string {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return values
}
