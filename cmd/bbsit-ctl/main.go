package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

const defaultSocket = "/run/bbsit/admin.sock"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	socket := os.Getenv("BBSIT_SOCKET")
	if socket == "" {
		socket = defaultSocket
	}
	c := newClient(socket)

	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "status", "projects":
		err = cmdStatus(c)
	case "history":
		err = cmdHistory(c, args)
	case "start":
		err = cmdAction(c, "start", args)
	case "stop":
		err = cmdAction(c, "stop", args)
	case "deploy":
		err = cmdAction(c, "deploy", args)
	case "rollback":
		err = cmdAction(c, "rollback", args)
	case "delete", "rm":
		err = cmdDelete(c, args)
	case "export":
		err = cmdExport(c, args)
	case "pack":
		err = cmdPack(c, args)
	case "unpack", "import":
		err = cmdUnpack(c, args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `bbsit-ctl - bbsit CLI

Commands:
  status                       Show all projects and their current state
  history <id>                 Show recent deployments for a project
  start <id>                   Start a project's stack
  stop <id>                    Stop a project's stack
  deploy <id>                  Trigger a manual deploy (poll + reconcile)
  rollback <id>                Roll back to the previous digest
  delete <id>                  Stop and remove a project
  export <id>                  Print the project definition as YAML
  pack <id> [-o file.tar.gz]   Pack project + persistent data into a tarball
  unpack <file|->              Restore a project from YAML/tar.gz file (or stdin if -)

Environment:
  BBSIT_SOCKET   Path to bbsit admin socket (default: /run/bbsit/admin.sock)`)
}

// --- HTTP-over-Unix-socket client ---

type client struct {
	hc *http.Client
}

func newClient(socket string) *client {
	return &client{
		hc: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socket)
				},
			},
			// No Timeout: long-running ops (pack of large data dirs, deploy
			// pulling large images) stream for arbitrary durations. Ctrl+C if needed.
		},
	}
}

func (c *client) do(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, "http://unix"+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			return nil, fmt.Errorf("admin socket not found — is bbsit running? (path: %s; override with BBSIT_SOCKET)", strings.TrimPrefix(err.Error(), "Get "))
		}
		if strings.Contains(err.Error(), "permission denied") {
			return nil, fmt.Errorf("permission denied accessing admin socket — add your user to the admin_group from bbsit config")
		}
		return nil, err
	}
	return resp, nil
}

func (c *client) getJSON(path string, out any) error {
	resp, err := c.do("GET", path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return apiError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *client) post(path string, body io.Reader, contentType string) error {
	resp, err := c.do("POST", path, body, contentType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return apiError(resp)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (c *client) delete(path string) error {
	resp, err := c.do("DELETE", path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return apiError(resp)
	}
	return nil
}

func apiError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var m map[string]string
	if json.Unmarshal(body, &m) == nil && m["error"] != "" {
		return fmt.Errorf("%s: %s", resp.Status, m["error"])
	}
	return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
}

// --- Command implementations ---

type projectWithState struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	State       struct {
		Status         string            `json:"status"`
		CurrentDigest  string            `json:"current_digest"`
		CurrentDigests map[string]string `json:"current_digests"`
		LastDeployAt   *time.Time        `json:"last_deploy_at"`
		LastError      string            `json:"last_error"`
	} `json:"state"`
}

func cmdStatus(c *client) error {
	var projects []projectWithState
	if err := c.getJSON("/api/projects", &projects); err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATUS\tDIGEST\tLAST DEPLOY\tERROR")
	for _, ps := range projects {
		digest := ps.State.CurrentDigest
		if digest == "" && len(ps.State.CurrentDigests) > 0 {
			// Show first service's digest for display
			for _, d := range ps.State.CurrentDigests {
				digest = d
				break
			}
		}
		if len(digest) > 19 {
			digest = digest[:19]
		}
		lastDeploy := "—"
		if ps.State.LastDeployAt != nil {
			lastDeploy = ps.State.LastDeployAt.Local().Format("01-02 15:04")
		}
		lastErr := ps.State.LastError
		if len(lastErr) > 40 {
			lastErr = lastErr[:40] + "…"
		}
		if lastErr == "" {
			lastErr = "—"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			ps.ID, ps.DisplayName, ps.State.Status, digest, lastDeploy, lastErr)
	}
	return w.Flush()
}

type projectDetail struct {
	Deployments []struct {
		ID           int64     `json:"id"`
		Trigger      string    `json:"trigger"`
		Status       string    `json:"status"`
		FromDigest   string    `json:"from_digest"`
		ToDigest     string    `json:"to_digest"`
		StartedAt    time.Time `json:"started_at"`
		ErrorMessage string    `json:"error_message"`
	} `json:"deployments"`
}

func cmdHistory(c *client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bbsit-ctl history <project-id>")
	}
	id := args[0]
	var detail projectDetail
	if err := c.getJSON("/api/projects/"+url.PathEscape(id), &detail); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTRIGGER\tSTATUS\tFROM\tTO\tSTARTED\tERROR")
	for _, d := range detail.Deployments {
		from := d.FromDigest
		if len(from) > 15 {
			from = from[:15]
		}
		to := d.ToDigest
		if len(to) > 15 {
			to = to[:15]
		}
		errMsg := d.ErrorMessage
		if len(errMsg) > 30 {
			errMsg = errMsg[:30] + "…"
		}
		if errMsg == "" {
			errMsg = "—"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			d.ID, d.Trigger, d.Status, from, to,
			d.StartedAt.Local().Format("01-02 15:04"), errMsg)
	}
	return w.Flush()
}

func cmdAction(c *client, action string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bbsit-ctl %s <project-id>", action)
	}
	id := args[0]
	if err := c.post("/api/projects/"+url.PathEscape(id)+"/"+action, nil, ""); err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", action, id)
	return nil
}

func cmdDelete(c *client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bbsit-ctl delete <project-id>")
	}
	id := args[0]
	if err := c.delete("/api/projects/" + url.PathEscape(id)); err != nil {
		return err
	}
	fmt.Printf("deleted: %s\n", id)
	return nil
}

func cmdExport(c *client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bbsit-ctl export <project-id>")
	}
	id := args[0]
	resp, err := c.do("GET", "/api/projects/"+url.PathEscape(id)+"/export", nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return apiError(resp)
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func cmdPack(c *client, args []string) error {
	var id, outPath string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-o", "--output":
			if i+1 >= len(args) {
				return fmt.Errorf("-o requires a path")
			}
			outPath = args[i+1]
			i++
		default:
			if id != "" {
				return fmt.Errorf("unexpected argument: %s", a)
			}
			id = a
		}
	}
	if id == "" {
		return fmt.Errorf("usage: bbsit-ctl pack <project-id> [-o file.tar.gz]")
	}

	fmt.Fprintf(os.Stderr, "packing %s...\n", id)
	resp, err := c.do("GET", "/api/projects/"+url.PathEscape(id)+"/export?format=tar.gz", nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return apiError(resp)
	}

	var out io.Writer = os.Stdout
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	} else if isTerminal(os.Stdout) {
		// Don't dump binary to a TTY
		return fmt.Errorf("refusing to write tarball to terminal — use -o <file>")
	}

	// Prefer Content-Length if the server provided it; otherwise show bytes only.
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	meter := newProgressMeter("pack", total)
	_, err = io.Copy(io.MultiWriter(out, meter), resp.Body)
	meter.Finish()
	if err != nil {
		return err
	}
	if outPath != "" {
		fmt.Fprintf(os.Stderr, "wrote: %s\n", outPath)
	}
	return nil
}

func cmdUnpack(c *client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bbsit-ctl unpack <file|->")
	}
	path := args[0]

	// Server auto-detects YAML vs gzip via magic bytes, so content-type only
	// matters when the request body is empty (it isn't here).
	var body io.Reader
	var total int64 // 0 if unknown (e.g. stdin)
	contentType := "application/octet-stream"
	if path == "-" {
		body = os.Stdin
		fmt.Fprintln(os.Stderr, "uploading from stdin...")
	} else {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if fi, err := f.Stat(); err == nil {
			total = fi.Size()
		}
		body = f
		contentType = "application/x-yaml"
		if strings.HasSuffix(path, ".tar.gz") || strings.HasSuffix(path, ".tgz") {
			contentType = "application/gzip"
		}
		fmt.Fprintf(os.Stderr, "uploading %s (%s)...\n", path, humanBytes(total))
	}

	meter := newProgressMeter("upload", total)
	body = io.TeeReader(body, meter)

	resp, err := c.do("POST", "/api/projects/import", body, contentType)
	meter.Finish()
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return apiError(resp)
	}
	fmt.Fprintln(os.Stderr, "server: extracting bundle and upserting project...")
	var p map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&p); err == nil {
		if id, ok := p["id"].(string); ok {
			fmt.Printf("imported: %s\n", id)
			return nil
		}
	}
	fmt.Println("imported")
	return nil
}

// progressMeter prints a one-line progress update to stderr at most once per
// tick. If total > 0, it includes a percentage and ETA. On a TTY it uses
// carriage returns to redraw in place; otherwise it emits a newline per tick.
type progressMeter struct {
	label   string
	total   int64 // 0 if unknown
	written int64
	start   time.Time
	last    time.Time
	tty     bool
}

func newProgressMeter(label string, total int64) *progressMeter {
	now := time.Now()
	return &progressMeter{
		label: label,
		total: total,
		start: now,
		last:  now.Add(-time.Second), // force first tick
		tty:   isTerminal(os.Stderr),
	}
}

func (p *progressMeter) Write(b []byte) (int, error) {
	n := len(b)
	p.written += int64(n)
	p.tick(false)
	return n, nil
}

func (p *progressMeter) tick(force bool) {
	now := time.Now()
	if !force && now.Sub(p.last) < time.Second {
		return
	}
	p.last = now
	elapsed := now.Sub(p.start).Seconds()
	rate := float64(p.written) / elapsed
	if elapsed < 0.05 {
		rate = 0
	}
	var line string
	if p.total > 0 {
		pct := 100 * float64(p.written) / float64(p.total)
		line = fmt.Sprintf("%s: %s / %s (%.1f%%) at %s/s",
			p.label, humanBytes(p.written), humanBytes(p.total), pct, humanBytes(int64(rate)))
	} else {
		line = fmt.Sprintf("%s: %s at %s/s", p.label, humanBytes(p.written), humanBytes(int64(rate)))
	}
	if p.tty {
		fmt.Fprintf(os.Stderr, "\r\033[K%s", line)
	} else {
		fmt.Fprintln(os.Stderr, line)
	}
}

func (p *progressMeter) Finish() {
	p.tick(true)
	elapsed := time.Since(p.start)
	if p.tty {
		fmt.Fprintln(os.Stderr) // end the in-place line
	}
	fmt.Fprintf(os.Stderr, "%s: done — %s in %s\n",
		p.label, humanBytes(p.written), elapsed.Round(time.Millisecond))
}

func humanBytes(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for x := n / u; x >= u; x /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// isTerminal reports whether f is connected to a terminal.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
