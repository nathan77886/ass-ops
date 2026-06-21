package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"assops/backend/internal/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "assops-tool:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := app.LoadConfig()
	api := flag.String("api", cfg.GatewayURL, "ASSOPS gateway URL")
	token := flag.String("token", os.Getenv("ASSOPS_TOKEN"), "gateway bearer token")
	contextDir := flag.String("context-dir", cfg.ContextDir, "local context directory")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "project":
		if len(args) == 2 && args[1] == "brief" {
			return readContextBrief(*contextDir)
		}
	case "repo":
		if len(args) == 2 && args[1] == "remotes" {
			return readContextKey(*contextDir, "remotes")
		}
	case "remote":
		if len(args) == 2 && args[1] == "actions" {
			return printJSON(map[string]any{"actions": []string{"repo.sync", "repo.tag", "github.actions.sync"}})
		}
	case "operations":
		if len(args) == 2 && args[1] == "recent" {
			return getAPI(*api, *token, "/api/operations")
		}
	case "plan":
		if len(args) == 2 && args[1] == "validate" {
			return printJSON(map[string]any{"valid": true, "message": "MVP validation accepts adapter plans"})
		}
	}
	return usage()
}

func usage() error {
	fmt.Fprintln(os.Stderr, "usage: assops-tool [--api URL] [--token TOKEN] <project brief|repo remotes|remote actions|operations recent|plan validate>")
	return fmt.Errorf("unknown command")
}

func readContextBrief(root string) error {
	path, err := firstContextFile(root, "ASSOPS_CONTEXT.md")
	if err != nil {
		return err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Print(string(bytes))
	return nil
}

func readContextKey(root, key string) error {
	path, err := firstContextFile(root, "assops-context.json")
	if err != nil {
		return err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(bytes, &data); err != nil {
		return err
	}
	return printJSON(data[key])
}

func firstContextFile(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}
		if !d.IsDir() && d.Name() == name {
			found = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("%s not found under %s", name, root)
	}
	return found, nil
}

func getAPI(base, token, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("gateway returned %s: %s", res.Status, string(body))
	}
	fmt.Println(string(body))
	return nil
}

func printJSON(value any) error {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(bytes))
	return nil
}
