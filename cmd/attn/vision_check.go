package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// maxVisionCheckBase64Bytes bounds the size of the base64-encoded image payload
// sent to the claude CLI. Past this size, prompt captures with --crop/--max-dim
// are a better fix than raising the limit.
const maxVisionCheckBase64Bytes = 4_500_000

// visionCheckResult is the parsed shape of the final `result` stream-json event
// emitted by `claude -p --output-format stream-json`.
type visionCheckResult struct {
	Result      string  `json:"result"`
	Subtype     string  `json:"subtype"`
	IsError     bool    `json:"is_error"`
	TotalCostUS float64 `json:"total_cost_usd"`
	NumTurns    int     `json:"num_turns"`
}

// visionCheckJSONOutput is the --json stdout shape.
type visionCheckJSONOutput struct {
	Answer   string  `json:"answer"`
	Model    string  `json:"model"`
	CostUSD  float64 `json:"cost_usd"`
	NumTurns int     `json:"num_turns"`
	IsError  bool    `json:"is_error"`
}

// mediaTypeForPath maps an image file extension to its MIME media type, as
// required by the claude CLI's image content block.
func mediaTypeForPath(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png", nil
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".gif":
		return "image/gif", nil
	case ".webp":
		return "image/webp", nil
	default:
		return "", fmt.Errorf("unsupported image extension %q (supported: .png, .jpg, .jpeg, .gif, .webp)", filepath.Ext(path))
	}
}

// buildVisionCheckMessage builds the single stream-json stdin line sent to
// `claude -p --input-format stream-json`: one user message with a text block
// (the question) followed by an image block (base64-encoded).
func buildVisionCheckMessage(question, mediaType, base64Data string) (string, error) {
	msg := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role": "user",
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": question,
				},
				{
					"type": "image",
					"source": map[string]interface{}{
						"type":       "base64",
						"media_type": mediaType,
						"data":       base64Data,
					},
				},
			},
		},
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseVisionCheckResult scans claude's stream-json stdout for the last line
// whose "type" is "result" and decodes it. Lines are read via a bufio.Reader
// (not bufio.Scanner) because assistant/tool lines carrying large payloads can
// exceed Scanner's default token size.
func parseVisionCheckResult(stdout string) (*visionCheckResult, error) {
	reader := bufio.NewReader(strings.NewReader(stdout))
	var last *visionCheckResult
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			var envelope struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(line), &envelope); err == nil && envelope.Type == "result" {
				var r visionCheckResult
				if err := json.Unmarshal([]byte(line), &r); err == nil {
					last = &r
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, readErr
		}
	}
	if last == nil {
		return nil, fmt.Errorf("no result event found in claude output")
	}
	return last, nil
}

// resolveClaudeBinary locates the claude CLI, checking PATH first and then a
// couple of common install locations that may not be on PATH for a
// non-interactive shell.
func resolveClaudeBinary() (string, error) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		for _, candidate := range []string{
			filepath.Join(home, ".npm-global", "bin", "claude"),
			filepath.Join(home, ".local", "bin", "claude"),
		} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("claude CLI not found on PATH, ~/.npm-global/bin, or ~/.local/bin")
}

func parseVisionCheckArgs(args []string) (image, question, model string, timeout time.Duration, jsonOut bool, err error) {
	fs := flag.NewFlagSet("vision-check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	modelFlag := fs.String("model", "sonnet", "model to use for the vision check")
	timeoutFlag := fs.Duration("timeout", 120*time.Second, "timeout for the claude CLI call")
	jsonFlag := fs.Bool("json", false, "print a single compact JSON line instead of the bare answer")

	positionals, perr := parseInterspersedFlagArgs(fs, args)
	if perr != nil {
		return "", "", "", 0, false, perr
	}
	if len(positionals) < 2 {
		return "", "", "", 0, false, fmt.Errorf("usage: attn vision-check <image> <question> [--model sonnet] [--timeout 120s] [--json]")
	}
	if len(positionals) > 2 {
		return "", "", "", 0, false, fmt.Errorf("unexpected extra arguments: %v", positionals[2:])
	}
	return positionals[0], positionals[1], *modelFlag, *timeoutFlag, *jsonFlag, nil
}

// runVisionCheck implements `attn vision-check <image> <question>`: a single
// tool-less LLM call that answers a question about an image without putting
// the image itself into the calling agent's context.
func runVisionCheck() {
	imagePath, question, model, timeout, jsonOut, err := parseVisionCheckArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "vision-check: %v\n", err)
		os.Exit(2)
	}

	mediaType, err := mediaTypeForPath(imagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vision-check: %v\n", err)
		os.Exit(1)
	}

	data, err := os.ReadFile(imagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vision-check: read %s: %v\n", imagePath, err)
		os.Exit(1)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	if len(encoded) > maxVisionCheckBase64Bytes {
		fmt.Fprintf(os.Stderr, "vision-check: encoded image is %d bytes, over the %d limit; capture with --crop/--max-dim first\n", len(encoded), maxVisionCheckBase64Bytes)
		os.Exit(1)
	}

	stdinLine, err := buildVisionCheckMessage(question, mediaType, encoded)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vision-check: build message: %v\n", err)
		os.Exit(1)
	}

	claudeBin, err := resolveClaudeBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vision-check: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, claudeBin,
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--model", model,
		"--max-turns", "1",
		"--strict-mcp-config",
		"--setting-sources", "",
		"--allowedTools", "",
	)
	cmd.Stdin = strings.NewReader(stdinLine + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	result, parseErr := parseVisionCheckResult(stdout.String())
	if parseErr != nil {
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "vision-check: claude CLI failed: %v\n%s\n", runErr, stderr.String())
		} else {
			fmt.Fprintf(os.Stderr, "vision-check: %v\n%s\n", parseErr, stderr.String())
		}
		os.Exit(1)
	}

	if result.IsError || result.Subtype != "success" {
		fmt.Fprintln(os.Stderr, result.Result)
		os.Exit(1)
	}

	if jsonOut {
		out := visionCheckJSONOutput{
			Answer:   result.Result,
			Model:    model,
			CostUSD:  result.TotalCostUS,
			NumTurns: result.NumTurns,
			IsError:  result.IsError,
		}
		b, err := json.Marshal(out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vision-check: encode json: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(b))
	} else {
		fmt.Println(result.Result)
		fmt.Fprintf(os.Stderr, "vision-check: model=%s cost_usd=%.4f turns=%d\n", model, result.TotalCostUS, result.NumTurns)
	}
}
