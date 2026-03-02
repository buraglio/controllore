// Package main is the entry point for the Controllore CLI client.
// The CLI is a pure API client — it does NOT embed any PCE logic.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	apiURL    string
	outputFmt string
)

func main() {
	root := &cobra.Command{
		Use:   "controllore",
		Short: "Controllore PCE CLI — SRv6/SR-MPLS Network Control",
		Long: `controllore is the CLI client for the Controllore PCE API.

Examples:
  controllore topology show
  controllore lsp list --type srv6
  controllore lsp create --src 192.0.2.1 --dst 192.0.2.2 --type srv6 --metric te
  controllore path compute --src 192.0.2.1 --dst 192.0.2.2 --usid
  controllore events watch`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			viper.SetEnvPrefix("CONTROLLORE")
			viper.AutomaticEnv()
			if apiURL == "" {
				apiURL = viper.GetString("API_URL")
				if apiURL == "" {
					apiURL = "http://localhost:8080"
				}
			}
		},
	}

	root.PersistentFlags().StringVarP(&apiURL, "api-url", "u", "", "Controllore API URL (env: CONTROLLORE_API_URL)")
	root.PersistentFlags().StringVarP(&outputFmt, "output", "o", "table", "Output format: table|json")

	// Subcommand groups
	root.AddCommand(configCmd())
	root.AddCommand(topologyCmd())
	root.AddCommand(lspCmd())
	root.AddCommand(pathCmd())
	root.AddCommand(sessionCmd())
	root.AddCommand(eventsCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── Config commands ────────────────────────────────────────────────────────

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage CLI configuration",
	}
	setURL := &cobra.Command{
		Use:   "set-url <url>",
		Short: "Set the API URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("API URL set to: %s\n", args[0])
			fmt.Println("Set CONTROLLORE_API_URL environment variable to persist.")
			return nil
		},
	}
	showCfg := &cobra.Command{
		Use: "show",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("API URL:", apiURL)
			return nil
		},
	}
	cmd.AddCommand(setURL, showCfg)
	return cmd
}

// ── Topology commands ──────────────────────────────────────────────────────

func topologyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "topology", Short: "Network topology operations"}

	show := &cobra.Command{
		Use:   "show",
		Short: "Show full topology snapshot",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/api/v1/topology")
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}

	nodes := &cobra.Command{
		Use:   "nodes",
		Short: "List all nodes in the TED",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/api/v1/topology/nodes")
			if err != nil {
				return err
			}
			var nodeList []map[string]interface{}
			if err := json.Unmarshal(data, &nodeList); err != nil || outputFmt == "json" {
				printJSON(data)
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ROUTER-ID\tHOSTNAME\tSRv6\tuSID\tMSD\tLOCATORS")
			for _, n := range nodeList {
				caps, _ := n["capabilities"].(map[string]interface{})
				srv6 := boolStr(caps["srv6_capable"])
				usid := boolStr(caps["srv6_usid_capable"])
				msd := fmt.Sprintf("%.0f", floatVal(caps["srv6_msd"]))
				locs, _ := n["srv6_locators"].([]interface{})
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n",
					strVal(n["router_id"]),
					strVal(n["hostname"]),
					srv6, usid, msd, len(locs))
			}
			w.Flush()
			return nil
		},
	}

	node := &cobra.Command{
		Use:   "node <router-id>",
		Short: "Show detailed info for a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/api/v1/topology/nodes/" + args[0])
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}

	segments := &cobra.Command{
		Use:   "segments",
		Short: "List all SRv6 SIDs/locators",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/api/v1/topology/segments")
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}

	cmd.AddCommand(show, nodes, node, segments)
	return cmd
}

// ── LSP commands ───────────────────────────────────────────────────────────

func lspCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "lsp", Short: "LSP lifecycle management"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List all PCE-controlled LSPs",
		RunE: func(cmd *cobra.Command, args []string) error {
			q := ""
			if t, _ := cmd.Flags().GetString("type"); t != "" {
				q += "?type=" + t
			}
			data, err := apiGet("/api/v1/lsps" + q)
			if err != nil {
				return err
			}
			var lsps []map[string]interface{}
			if err := json.Unmarshal(data, &lsps); err != nil || outputFmt == "json" {
				printJSON(data)
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tSRC\tDST\tTYPE\tSTATUS\tSIDs\tMETRIC")
			for _, l := range lsps {
				segs, _ := l["segment_list"].([]interface{})
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%.0f\n",
					shortID(strVal(l["id"])),
					strVal(l["name"]),
					strVal(l["src"]),
					strVal(l["dst"]),
					strVal(l["sr_type"]),
					strVal(l["status"]),
					len(segs),
					floatVal(l["computed_metric"]))
			}
			w.Flush()
			return nil
		},
	}
	list.Flags().String("type", "", "Filter by type: srv6|mpls")
	list.Flags().String("pcc", "", "Filter by PCC router-id")

	show := &cobra.Command{
		Use:   "show <id>",
		Short: "Show detailed LSP info including segment list",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/api/v1/lsps/" + args[0])
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}

	var (
		lspName     string
		lspSrc      string
		lspDst      string
		lspType     string
		lspMetric   string
		lspBW       uint64
		lspFlexAlgo uint8
		lspUSID     bool
	)
	create := &cobra.Command{
		Use:   "create",
		Short: "Create and initiate a new PCE LSP",
		RunE: func(cmd *cobra.Command, args []string) error {
			metricMap := map[string]int{"igp": 0, "te": 1, "latency": 2, "hopcount": 3}
			mt := metricMap[lspMetric]

			reqBody := map[string]interface{}{
				"name":    lspName,
				"src":     lspSrc,
				"dst":     lspDst,
				"sr_type": lspType,
				"constraints": map[string]interface{}{
					"metric_type":   mt,
					"min_bandwidth": lspBW,
					"flex_algo":     lspFlexAlgo,
					"use_usid":      lspUSID,
				},
			}
			data, err := apiPost("/api/v1/lsps", reqBody)
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
	create.Flags().StringVar(&lspName, "name", "", "LSP name")
	create.Flags().StringVar(&lspSrc, "src", "", "Source router-id (required)")
	create.Flags().StringVar(&lspDst, "dst", "", "Destination router-id (required)")
	create.Flags().StringVar(&lspType, "type", "srv6", "SR type: srv6|mpls")
	create.Flags().StringVar(&lspMetric, "metric", "te", "Metric: igp|te|latency|hopcount")
	create.Flags().Uint64Var(&lspBW, "bw", 0, "Minimum bandwidth (bytes/sec)")
	create.Flags().Uint8Var(&lspFlexAlgo, "flex-algo", 0, "Flex-algorithm (128-255, 0=default)")
	create.Flags().BoolVar(&lspUSID, "usid", false, "Use SRv6 uSID compression")

	history := &cobra.Command{
		Use:   "history <id>",
		Short: "Show LSP change history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/api/v1/lsps/" + args[0] + "/history")
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}

	del := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete (teardown) a PCE-controlled LSP",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return apiDelete("/api/v1/lsps/" + args[0])
		},
	}

	cmd.AddCommand(list, show, create, history, del)
	return cmd
}

// ── Path commands ──────────────────────────────────────────────────────────

func pathCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "path", Short: "Path computation (non-instantiating)"}

	var (
		pathSrc      string
		pathDst      string
		pathMetric   string
		pathUSID     bool
		pathFlexAlgo uint8
	)
	compute := &cobra.Command{
		Use:   "compute",
		Short: "Compute an SRv6 path without instantiating it",
		RunE: func(cmd *cobra.Command, args []string) error {
			metricMap := map[string]int{"igp": 0, "te": 1, "latency": 2, "hopcount": 3}
			reqBody := map[string]interface{}{
				"src": pathSrc,
				"dst": pathDst,
				"constraints": map[string]interface{}{
					"metric_type": metricMap[pathMetric],
					"use_usid":    pathUSID,
					"flex_algo":   pathFlexAlgo,
				},
			}
			data, err := apiPost("/api/v1/paths/compute", reqBody)
			if err != nil {
				return err
			}
			var result map[string]interface{}
			json.Unmarshal(data, &result)
			fmt.Printf("Cost:       %.0f (%s)\n", floatVal(result["cost"]), strVal(result["metric_type"]))
			fmt.Printf("SID Count:  %.0f\n", floatVal(result["sid_count"]))
			hops, _ := result["node_hops"].([]interface{})
			fmt.Println("Node Hops:")
			for i, h := range hops {
				fmt.Printf("  %d. %v\n", i+1, h)
			}
			segs, _ := result["segment_list"].([]interface{})
			fmt.Println("Segment List:")
			for i, s := range segs {
				seg, _ := s.(map[string]interface{})
				fmt.Printf("  %d. [%s] %v\n", i+1, strVal(seg["type"]), seg["addr"])
			}
			return nil
		},
	}
	compute.Flags().StringVar(&pathSrc, "src", "", "Source router-id (required)")
	compute.Flags().StringVar(&pathDst, "dst", "", "Destination router-id (required)")
	compute.Flags().StringVar(&pathMetric, "metric", "te", "Metric: igp|te|latency|hopcount")
	compute.Flags().BoolVar(&pathUSID, "usid", false, "Compute with uSID compression")
	compute.Flags().Uint8Var(&pathFlexAlgo, "flex-algo", 0, "Flex-algorithm domain")

	cmd.AddCommand(compute)
	return cmd
}

// ── Session commands ───────────────────────────────────────────────────────

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "session", Short: "PCEP session management"}

	list := &cobra.Command{
		Use: "list",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiGet("/api/v1/sessions")
			if err != nil {
				return err
			}
			var sessions []map[string]interface{}
			if err := json.Unmarshal(data, &sessions); err != nil || outputFmt == "json" {
				printJSON(data)
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tPEER\tSTATE\tSRv6\tuSID\tMSD\tMSG-RX\tMSG-TX")
			for _, s := range sessions {
				caps, _ := s["capabilities"].(map[string]interface{})
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%v\t%.0f\t%.0f\n",
					shortID(strVal(s["id"])),
					strVal(s["peer_addr"]),
					strVal(s["state"]),
					boolStr(caps["srv6_capable"]),
					boolStr(caps["srv6_usid_capable"]),
					caps["srv6_msd"],
					floatVal(s["msgs_rx"]),
					floatVal(s["msgs_tx"]))
			}
			w.Flush()
			return nil
		},
	}
	cmd.AddCommand(list)
	return cmd
}

// ── Events commands ────────────────────────────────────────────────────────

func eventsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "events", Short: "Real-time event feed"}

	watch := &cobra.Command{
		Use:   "watch",
		Short: "Stream live PCE events via WebSocket",
		RunE: func(cmd *cobra.Command, args []string) error {
			wsURL := strings.Replace(apiURL, "http://", "ws://", 1)
			wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
			wsURL += "/ws/events"
			fmt.Printf("Connecting to %s ...\n", wsURL)
			// Simple HTTP long-poll fallback (WebSocket requires additional dep)
			// For a real implementation: use golang.org/x/net/websocket or gorilla/websocket
			fmt.Println("Note: Install gorilla/websocket in the CLI for full streaming support.")
			fmt.Println("Polling /api/v1/health for now:")
			for {
				data, err := apiGet("/api/v1/health")
				if err != nil {
					log.Error().Err(err).Msg("health check failed")
				} else {
					fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), string(data))
				}
				time.Sleep(5 * time.Second)
			}
		},
	}
	cmd.AddCommand(watch)
	return cmd
}

// ── HTTP helpers ───────────────────────────────────────────────────────────

func apiGet(path string) ([]byte, error) {
	resp, err := http.Get(apiURL + path)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func apiPost(path string, payload interface{}) ([]byte, error) {
	b, _ := json.Marshal(payload)
	resp, err := http.Post(apiURL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func apiDelete(path string) error {
	req, _ := http.NewRequest("DELETE", apiURL+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	fmt.Println("Deleted successfully.")
	return nil
}

func printJSON(data []byte) {
	var out bytes.Buffer
	json.Indent(&out, data, "", "  ")
	fmt.Println(out.String())
}

// Helpers for table output
func strVal(v interface{}) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%v", v)
}
func floatVal(v interface{}) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}
func boolStr(v interface{}) string {
	if b, ok := v.(bool); ok && b {
		return "✓"
	}
	return "-"
}
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
