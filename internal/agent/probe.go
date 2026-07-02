package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
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
		if target.Type != "tcping" && target.Type != "tcp" {
			rounds = append(rounds, ProbeRound{TargetID: target.ID, TS: ts, Type: target.Type, Samples: failedProbeSamples(target.Count, fmt.Sprintf("unsupported_%s", target.Type))})
			continue
		}
		rounds = append(rounds, ProbeRound{TargetID: target.ID, TS: ts, Type: target.Type, Samples: RunTCPProbe(ctx, target)})
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
