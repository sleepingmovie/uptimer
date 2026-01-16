package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Reset  = "\033[0m"

	maxBackoff    = 5 * time.Minute
	backoffFactor = 2
	certWarnDays  = 30
)

var (
	wait_time      int
	show_ok        bool
	show_rt        bool
	sound_alert    bool
	no_window      bool
	dashboard_port string
	client         = &http.Client{Timeout: 30 * time.Second}

	endpoints   = make(map[string]*EndpointStats)
	endpointsMu sync.RWMutex
	startTime   = time.Now()
)

type EndpointStats struct {
	URL              string    `json:"url"`
	ExpectedCode     string    `json:"expected_code"`
	TotalChecks      int64     `json:"total_checks"`
	SuccessfulChecks int64     `json:"successful_checks"`
	ConsecFailures   int       `json:"consecutive_failures"`
	LastCheck        time.Time `json:"last_check"`
	LastStatus       string    `json:"last_status"`
	LastResponseTime int64     `json:"last_response_time_ms"`
	CertExpiry       time.Time `json:"cert_expiry,omitempty"`
	IsUp             bool      `json:"is_up"`
	mu               sync.Mutex
}

func main() {
	showOkFlag := flag.Bool("so", false, "show ok answers")
	showRtFlag := flag.Bool("rt", false, "show response time")
	soundAlertFlag := flag.Bool("sa", false, "sound alert on failure")
	dashboardFlag := flag.String("dp", "", "dashboard port (e.g., 8080)")
	noWindowFlag := flag.Bool("nw", false, "no window (requires -dp)")
	flag.Parse()
	show_ok = *showOkFlag
	show_rt = *showRtFlag
	sound_alert = *soundAlertFlag
	dashboard_port = *dashboardFlag
	no_window = *noWindowFlag

	if no_window && dashboard_port == "" {
		color_print(Red, "Error: -nw flag requires -dp flag to be set")
		os.Exit(1)
	}

	if no_window {
		hideConsoleWindow()
	}

	file, err := os.Open("endpoints.txt")
	if err != nil {
		_, err := os.Create("endpoints.txt")
		if err != nil {
			panic(err)
		}
		color_print(Green, "endpoints.txt file was created!\nFill out the file to use the program")
		os.Exit(1)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	if scanner.Scan() {
		line := scanner.Text()
		num, err := strconv.Atoi(line)
		if err != nil {
			color_print(Red, "Wait time not found. Set to default 10 seconds")
			wait_time = 10
			regex_to_handle(line)
		} else {
			color_printf(Green, "Wait time is %d seconds\n", num)
			wait_time = num
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		regex_to_handle(line)
	}

	if dashboard_port != "" {
		go startDashboard(dashboard_port)
		log_printf(Green, "Dashboard running at http://localhost:%s\n", dashboard_port)
	}

	log_print(Green, "Listening...")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	printShutdownSummary()
}

func regex_to_handle(line string) {
	re := regexp.MustCompile(`^(https?://[a-zA-Z0-9._-]+(:\d+)?(?:/[^\s]*)?)\s*(\d{3})?$`)
	if line == "" {
		return
	}
	m := re.FindStringSubmatch(line)
	if m != nil {
		url := m[1]
		code := m[3]
		if code == "" {
			code = "200"
		}
		stats := &EndpointStats{
			URL:          url,
			ExpectedCode: code,
			IsUp:         true,
		}
		endpointsMu.Lock()
		endpoints[url] = stats
		endpointsMu.Unlock()

		go handle_endpoint(stats)
	} else {
		log_printf(Red, "%s line is incorrect!\n", line)
	}
}

func handle_endpoint(stats *EndpointStats) {
	currentBackoff := time.Duration(wait_time) * time.Second
	normalInterval := currentBackoff
	link := stats.URL
	awaited_answer := stats.ExpectedCode

	if len(link) > 5 && link[:5] == "https" {
		checkSSLCert(link, stats)
	}

	for {
		start := time.Now()
		resp, err := client.Get(link)
		responseTime := time.Since(start)

		stats.mu.Lock()
		stats.TotalChecks++
		stats.LastCheck = time.Now()
		stats.LastResponseTime = responseTime.Milliseconds()

		if err != nil {
			stats.ConsecFailures++
			stats.IsUp = false
			stats.LastStatus = "ERROR"
			stats.mu.Unlock()

			playAlert()
			log_printf(Red, "%s - ERROR: %v (failures: %d, retry in %v)\n", link, err, stats.ConsecFailures, currentBackoff)
			time.Sleep(currentBackoff)
			currentBackoff = increaseBackoff(currentBackoff)
			continue
		}
		resp.Body.Close()

		rtSuffix := ""
		if show_rt {
			rtSuffix = fmt.Sprintf(" [%v]", responseTime.Round(time.Millisecond))
		}

		answer := strconv.Itoa(resp.StatusCode)
		stats.LastStatus = answer

		if answer != awaited_answer {
			stats.ConsecFailures++
			stats.IsUp = false
			stats.mu.Unlock()

			playAlert()
			log_printf(Red, "%s HAS RETURNED %s INSTEAD OF %s - POSSIBLE DOWN!!%s (failures: %d, retry in %v)\n",
				link, answer, awaited_answer, rtSuffix, stats.ConsecFailures, currentBackoff)
			time.Sleep(currentBackoff)
			currentBackoff = increaseBackoff(currentBackoff)
		} else {
			stats.SuccessfulChecks++
			stats.ConsecFailures = 0
			stats.IsUp = true
			stats.mu.Unlock()

			if show_ok {
				log_printf(Green, "%s - %s AS EXPECTED%s\n", link, answer, rtSuffix)
			}
			currentBackoff = normalInterval
			time.Sleep(normalInterval)
		}
	}
}

func checkSSLCert(link string, stats *EndpointStats) {
	host := link[8:]
	for i, c := range host {
		if c == '/' || c == ':' {
			host = host[:i]
			break
		}
	}

	conn, err := tls.Dial("tcp", host+":443", &tls.Config{})
	if err != nil {
		log_printf(Yellow, "%s - SSL cert check failed: %v\n", link, err)
		return
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) > 0 {
		expiry := certs[0].NotAfter
		stats.mu.Lock()
		stats.CertExpiry = expiry
		stats.mu.Unlock()

		daysUntilExpiry := int(time.Until(expiry).Hours() / 24)
		if daysUntilExpiry <= certWarnDays {
			playAlert()
			log_printf(Yellow, "%s - SSL cert expires in %d days (%s)\n", link, daysUntilExpiry, expiry.Format("2006-01-02"))
		} else if show_ok {
			log_printf(Green, "%s - SSL cert valid for %d days\n", link, daysUntilExpiry)
		}
	}
}

func increaseBackoff(current time.Duration) time.Duration {
	next := current * backoffFactor
	if next > maxBackoff {
		return maxBackoff
	}
	return next
}

func playAlert() {
	if sound_alert {
		beep := syscall.NewLazyDLL("kernel32.dll").NewProc("Beep")
		beep.Call(750, 300)
	}
}

func hideConsoleWindow() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	user32 := syscall.NewLazyDLL("user32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	showWindow := user32.NewProc("ShowWindow")

	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd != 0 {
		showWindow.Call(hwnd, 0)
	}
}

func printShutdownSummary() {
	fmt.Println("\n" + Yellow + "========== SHUTDOWN SUMMARY ==========" + Reset)
	uptime := time.Since(startTime).Round(time.Second)
	fmt.Printf("Total uptime: %v\n\n", uptime)

	endpointsMu.RLock()
	defer endpointsMu.RUnlock()

	for _, stats := range endpoints {
		stats.mu.Lock()
		uptimePercent := float64(0)
		if stats.TotalChecks > 0 {
			uptimePercent = float64(stats.SuccessfulChecks) / float64(stats.TotalChecks) * 100
		}
		status := Green + "UP" + Reset
		if !stats.IsUp {
			status = Red + "DOWN" + Reset
		}
		fmt.Printf("%s\n", stats.URL)
		fmt.Printf("  Status: %s | Uptime: %.2f%% | Checks: %d/%d | Consec Failures: %d\n",
			status, uptimePercent, stats.SuccessfulChecks, stats.TotalChecks, stats.ConsecFailures)
		if !stats.CertExpiry.IsZero() {
			fmt.Printf("  SSL Cert Expires: %s\n", stats.CertExpiry.Format("2006-01-02"))
		}
		stats.mu.Unlock()
	}
	fmt.Println(Yellow + "======================================" + Reset)
}

func timestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func log_print(color, text string) {
	fmt.Printf("[%s] %s%s%s\n", timestamp(), color, text, Reset)
}

func log_printf(color, format string, a ...any) {
	fmt.Printf("[%s] %s"+format+Reset, append([]any{timestamp(), color}, a...)...)
}

func color_print(color, text string) {
	fmt.Println(color + text + Reset)
}

func color_printf(color, format string, a ...any) {
	fmt.Printf(color+format+Reset, a...)
}

func startDashboard(port string) {
	http.HandleFunc("/", dashboardHandler)
	http.HandleFunc("/api/status", apiStatusHandler)
	http.ListenAndServe(":"+port, nil)
}

func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html>
<head>
	<title>Uptimer Dashboard</title>
	<meta http-equiv="refresh" content="5">
	<style>
		body { font-family: Arial, sans-serif; margin: 20px; background: #1a1a2e; color: #eee; }
		h1 { color: #00d4ff; }
		table { border-collapse: collapse; width: 100%%; margin-top: 20px; }
		th, td { border: 1px solid #444; padding: 12px; text-align: left; }
		th { background: #16213e; }
		tr:nth-child(even) { background: #1a1a2e; }
		tr:nth-child(odd) { background: #16213e; }
		.up { color: #00ff88; font-weight: bold; }
		.down { color: #ff4444; font-weight: bold; }
		.warn { color: #ffaa00; }
		.uptime-good { color: #00ff88; }
		.uptime-warn { color: #ffaa00; }
		.uptime-bad { color: #ff4444; }
	</style>
</head>
<body>
	<h1>Uptimer Dashboard</h1>
	<p>Monitoring since: %s | Uptime: %s</p>
	<table>
		<tr>
			<th>Endpoint</th>
			<th>Status</th>
			<th>Last Code</th>
			<th>Response Time</th>
			<th>Uptime</th>
			<th>Checks</th>
			<th>Failures</th>
			<th>SSL Expiry</th>
			<th>Last Check</th>
		</tr>
		%s
	</table>
	<p><small>Auto-refreshes every 5 seconds. API available at <a href="/api/status">/api/status</a></small></p>
</body>
</html>`

	var rows string
	endpointsMu.RLock()
	for _, stats := range endpoints {
		stats.mu.Lock()
		statusClass := "up"
		statusText := "UP"
		if !stats.IsUp {
			statusClass = "down"
			statusText = "DOWN"
		}

		uptimePercent := float64(0)
		if stats.TotalChecks > 0 {
			uptimePercent = float64(stats.SuccessfulChecks) / float64(stats.TotalChecks) * 100
		}
		uptimeClass := "uptime-good"
		if uptimePercent < 99 {
			uptimeClass = "uptime-warn"
		}
		if uptimePercent < 95 {
			uptimeClass = "uptime-bad"
		}

		certExpiry := "-"
		if !stats.CertExpiry.IsZero() {
			daysLeft := int(time.Until(stats.CertExpiry).Hours() / 24)
			certClass := ""
			if daysLeft <= certWarnDays {
				certClass = "class=\"warn\""
			}
			certExpiry = fmt.Sprintf("<span %s>%s (%dd)</span>", certClass, stats.CertExpiry.Format("2006-01-02"), daysLeft)
		}

		lastCheck := "-"
		if !stats.LastCheck.IsZero() {
			lastCheck = stats.LastCheck.Format("15:04:05")
		}

		rows += fmt.Sprintf(`<tr>
			<td>%s</td>
			<td class="%s">%s</td>
			<td>%s (expect %s)</td>
			<td>%dms</td>
			<td class="%s">%.2f%%</td>
			<td>%d</td>
			<td>%d</td>
			<td>%s</td>
			<td>%s</td>
		</tr>`,
			stats.URL, statusClass, statusText, stats.LastStatus, stats.ExpectedCode,
			stats.LastResponseTime, uptimeClass, uptimePercent, stats.TotalChecks,
			stats.ConsecFailures, certExpiry, lastCheck)
		stats.mu.Unlock()
	}
	endpointsMu.RUnlock()

	uptime := time.Since(startTime).Round(time.Second)
	fmt.Fprintf(w, html, startTime.Format("2006-01-02 15:04:05"), uptime, rows)
}

func apiStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	endpointsMu.RLock()
	defer endpointsMu.RUnlock()

	var statsList []*EndpointStats
	for _, stats := range endpoints {
		statsList = append(statsList, stats)
	}

	response := struct {
		StartTime string           `json:"start_time"`
		Uptime    string           `json:"uptime"`
		Endpoints []*EndpointStats `json:"endpoints"`
	}{
		StartTime: startTime.Format(time.RFC3339),
		Uptime:    time.Since(startTime).Round(time.Second).String(),
		Endpoints: statsList,
	}

	json.NewEncoder(w).Encode(response)
}
