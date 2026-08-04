// Command e2e-spike is a throwaway proof-of-concept: can osac-sp's e2e suite
// import and drive osac-project/fulfillment-service's own `it` package (its
// kind-based integration-test harness: real fulfillment-service + real
// Keycloak + real Postgres, no AAP/no real hub) instead of hand-rolling an
// equivalent? See README.md for full context; this is not production code.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	it "github.com/osac-project/fulfillment-service/it"
)

func main() {
	os.Exit(run())
}

func run() int {
	projectDir := flag.String("projectdir", "", "path to a fulfillment-service checkout (required for -runsetup)")
	runSetup := flag.Bool("runsetup", false, "actually call tool.Setup(ctx)/Cleanup(ctx) against a real fulfillment-service checkout (needs kind+docker/podman+helm)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fmt.Println("=== Step 1: does `it.NewTool()...Build()` construct without error? ===")
	builder := it.NewTool().
		SetLogger(logger).
		SetKeepCluster(true).
		SetKeepService(true).
		SetDebug(false).
		SetSecret("spike-secret").
		SetCaFiles("", "")
	if *projectDir != "" {
		builder = builder.SetProjectDir(*projectDir)
		builder = builder.
			AddCrdFile(filepath.Join("it", "crds", "clusterorders.osac.openshift.io.yaml")).
			AddCrdFile(filepath.Join("it", "crds", "hostedclusters.hypershift.openshift.io.yaml")).
			AddCrdFile(filepath.Join("it", "crds", "tenants.osac.openshift.io.yaml")).
			AddCrdFile(filepath.Join("it", "crds", "osac.openshift.io_baremetalinstances.yaml"))
	}

	tool, err := builder.Build()
	if err != nil {
		fmt.Printf("FAIL: it.NewTool()...Build() returned an error: %v\n", err)
		return 1
	}
	fmt.Println("PASS: it.NewTool()...Build() constructed a *it.Tool successfully.")
	fmt.Printf("      (this alone proves the `it` package resolves and compiles as an\n")
	fmt.Printf("       external module dependency, not just from inside its own repo)\n")

	if !*runSetup {
		fmt.Println()
		fmt.Println("Skipping tool.Setup(ctx) (pass -runsetup -projectdir=<path to a real")
		fmt.Println("fulfillment-service checkout> to attempt the real kind+Keycloak+Postgres")
		fmt.Println("+fulfillment-service bring-up). See README.md for why -projectdir is")
		fmt.Println("required: it.Tool resolves relative chart/CRD/Containerfile paths from")
		fmt.Println("its own project root, discovered by walking up from CWD for a go.mod -")
		fmt.Println("which, for an external caller, is *our* go.mod, not fulfillment-service's.")
		return 0
	}

	if *projectDir == "" {
		fmt.Println("FAIL: -runsetup requires -projectdir=<path to a real fulfillment-service checkout>")
		return 1
	}

	fmt.Println()
	fmt.Println("=== Step 2: does tool.Setup(ctx) actually bring up kind+Keycloak+Postgres+fulfillment-service? ===")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	start := time.Now()
	if err := tool.Setup(ctx); err != nil {
		fmt.Printf("FAIL: tool.Setup(ctx) returned an error after %s: %v\n", time.Since(start), err)
		return 1
	}
	fmt.Printf("PASS: tool.Setup(ctx) completed in %s.\n", time.Since(start))

	defer func() {
		fmt.Println("Cleaning up...")
		if err := tool.Cleanup(context.Background()); err != nil {
			fmt.Printf("WARNING: tool.Cleanup(ctx) returned an error: %v\n", err)
		}
	}()

	return 0
}
