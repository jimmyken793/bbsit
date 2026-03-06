package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/kingyoung/bbsit/internal/db"
)

const defaultDBPath = "/opt/bbsit/state.db"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	dbPath := os.Getenv("BBSIT_DB")
	if dbPath == "" {
		dbPath = defaultDBPath
	}

	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	switch os.Args[1] {
	case "status":
		cmdStatus(database)
	case "projects":
		cmdStatus(database) // alias
	case "history":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: bbsit-ctl history <project-id>")
			os.Exit(1)
		}
		cmdHistory(database, os.Args[2])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `bbsit-ctl - bbsit CLI

Commands:
  status              Show all projects and their current state
  projects            Alias for status
  history <id>        Show deployment history for a project

Environment:
  BBSIT_DB     Path to SQLite database (default: /opt/bbsit/state.db)`)
}

func cmdStatus(database *db.DB) {
	projects, err := database.ListProjectsWithState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATUS\tDIGEST\tLAST DEPLOY\tERROR")
	for _, ps := range projects {
		digest := ps.State.CurrentDigest
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
	w.Flush()
}

func cmdHistory(database *db.DB, projectID string) {
	deployments, err := database.ListDeployments(projectID, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTRIGGER\tSTATUS\tFROM\tTO\tSTARTED\tERROR")
	for _, d := range deployments {
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
	w.Flush()
}
