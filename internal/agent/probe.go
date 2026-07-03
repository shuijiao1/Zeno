package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func RunTCPProbe(ctx context.Context, target ProbeTarget) []ProbeSample {
	count := target.Count
	if count <= 0 {
		count = 1
	}
	timeout := time.Duration(target.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Second
	}
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
		dialCtx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		conn, err := (&net.Dialer{Timeout: timeout}).DialContext(dialCtx, "tcp", address)
		elapsedMS := float64(time.Since(start).Microseconds()) / 1000
		cancel()
		if err != nil {
			samples = append(samples, ProbeSample{Seq: seq, Success: false, Error: classifyProbeError(err)})
			continue
		}
		_ = conn.Close()
		latency := elapsedMS
		samples = append(samples, ProbeSample{Seq: seq, Success: true, LatencyMS: &latency})
	}
	return samples
}

var pingLatencyPattern = regexp.MustCompile(`time[=<]([0-9.]+)\s*ms`)

func RunPingProbe(ctx context.Context, target ProbeTarget) []ProbeSample {
	count := target.Count
	if count <= 0 {
		count = 1
	}
	timeout := time.Duration(target.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Second
	}
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

		attemptCtx, cancel := context.WithTimeout(ctx, timeout+time.Second)
		start := time.Now()
		cmd := exec.CommandContext(attemptCtx, "ping", "-n", "-c", "1", "-W", strconv.Itoa(pingTimeoutSeconds(timeout)), address)
		output, err := cmd.CombinedOutput()
		elapsedMS := float64(time.Since(start).Microseconds()) / 1000
		ctxErr := attemptCtx.Err()
		cancel()
		if err != nil {
			samples = append(samples, ProbeSample{Seq: seq, Success: false, Error: classifyPingError(err, string(output), ctxErr)})
			continue
		}
		latency := elapsedMS
		if parsed, ok := parsePingLatencyMS(string(output)); ok {
			latency = parsed
		}
		samples = append(samples, ProbeSample{Seq: seq, Success: true, LatencyMS: &latency})
	}
	return samples
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
	rounds := make([]ProbeRound, 0, len(targets))
	for _, target := range targets {
		switch target.Type {
		case "tcping", "tcp":
			rounds = append(rounds, ProbeRound{TargetID: target.ID, TS: ts, Type: target.Type, Samples: RunTCPProbe(ctx, target)})
		case "ping", "icmp":
			rounds = append(rounds, ProbeRound{TargetID: target.ID, TS: ts, Type: target.Type, Samples: RunPingProbe(ctx, target)})
		default:
			rounds = append(rounds, ProbeRound{TargetID: target.ID, TS: ts, Type: target.Type, Samples: failedProbeSamples(target.Count, fmt.Sprintf("unsupported_%s", target.Type))})
		}
	}
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
