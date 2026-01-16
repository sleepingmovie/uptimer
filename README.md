# Uptimer

A lightweight, concurrent HTTP/HTTPS endpoint monitoring tool written in Go. Monitor multiple endpoints simultaneously with real-time status updates, SSL certificate expiry warnings, and an optional web dashboard.

## Features

- **Concurrent Monitoring**: Monitor multiple endpoints simultaneously using goroutines
- **HTTP Status Validation**: Check endpoints return expected status codes
- **SSL Certificate Monitoring**: Automatic warnings when certificates expire within 30 days
- **Exponential Backoff**: Smart retry logic with increasing delays on failures (up to 5 minutes max)
- **Web Dashboard**: Optional real-time HTML dashboard with auto-refresh
- **JSON API**: Programmatic access to monitoring data
- **Sound Alerts**: Optional audible alerts on failures (Windows)
- **Colored Console Output**: Easy-to-read status messages with color coding
- **Shutdown Summary**: Statistics report when the program exits

## Installation

```bash
go build -o uptimer.exe uptimer.go
```

## Configuration

### endpoints.txt

Create an `endpoints.txt` file in the same directory as the executable. The file format is:

```
[wait_time_in_seconds]
url [expected_status_code]
url [expected_status_code]
...
```

**Format details:**
- **Line 1** (optional): Wait time between checks in seconds. If omitted or invalid, defaults to 10 seconds.
- **Subsequent lines**: One endpoint per line with format `URL [STATUS_CODE]`
  - URL must start with `http://` or `https://`
  - Status code is optional, defaults to `200`

**Supported URL formats:**
- Domain names: `https://example.com`, `https://sub.example.com`
- IP addresses: `http://192.168.1.1`, `http://10.0.0.1`
- Localhost: `http://localhost`, `https://localhost`
- Custom ports: `http://localhost:3000`, `http://192.168.1.1:8080`
- With paths: `http://localhost:3000/api/health`

### Example endpoints.txt

```
30
https://example.com
https://api.example.com/health 200
https://legacy.example.com/status 301
http://localhost:3000 200
http://192.168.1.100:8080/ping
http://127.0.0.1:5000/api/health 201
```

This configuration:
- Checks endpoints every 30 seconds
- Expects `200` for example.com, api.example.com, and localhost:3000
- Expects `301` for legacy.example.com
- Expects `200` for 192.168.1.100:8080
- Expects `201` for 127.0.0.1:5000

## Usage

### Basic Usage

```bash
uptimer.exe
```

### Command-Line Flags

| Flag | Description |
|------|-------------|
| `-so` | **Show OK**: Display successful check messages (silent by default) |
| `-rt` | **Response Time**: Show response time for each check |
| `-sa` | **Sound Alert**: Play an audible beep on failures (Windows only) |
| `-dp PORT` | **Dashboard Port**: Enable web dashboard on specified port |
| `-nw` | **No Window**: Hide console window (requires `-dp` to be set) |

### Examples

**Show all responses with timing:**
```bash
uptimer.exe -so -rt
```

**Enable web dashboard on port 8080:**
```bash
uptimer.exe -dp 8080
```

**Run as background service with dashboard only:**
```bash
uptimer.exe -dp 8080 -nw
```

**Full monitoring with alerts:**
```bash
uptimer.exe -so -rt -sa -dp 8080
```

## Web Dashboard

When enabled with `-dp`, access the dashboard at `http://localhost:PORT`

### Dashboard Features

- Real-time status of all monitored endpoints
- Auto-refreshes every 5 seconds
- Shows for each endpoint:
  - Current status (UP/DOWN)
  - Last HTTP status code
  - Response time
  - Uptime percentage
  - Total checks performed
  - Consecutive failures
  - SSL certificate expiry date
  - Last check timestamp

### JSON API

Access monitoring data programmatically at `http://localhost:PORT/api/status`

**Response format:**
```json
{
  "start_time": "2024-01-15T10:30:00Z",
  "uptime": "2h15m30s",
  "endpoints": [
    {
      "url": "https://example.com",
      "expected_code": "200",
      "total_checks": 150,
      "successful_checks": 148,
      "consecutive_failures": 0,
      "last_check": "2024-01-15T12:45:30Z",
      "last_status": "200",
      "last_response_time_ms": 245,
      "cert_expiry": "2024-06-15T00:00:00Z",
      "is_up": true
    }
  ]
}
```

## Behavior

### Monitoring Logic

1. Each endpoint is monitored in its own goroutine
2. On success: waits the configured interval before next check
3. On failure: applies exponential backoff (2x multiplier, max 5 minutes)
4. Backoff resets to normal interval after a successful check

### SSL Certificate Checks

- Performed once at startup for HTTPS endpoints
- Warns if certificate expires within 30 days
- Expiry date shown in dashboard and shutdown summary

### Console Output

- **Green**: Successful checks, informational messages
- **Yellow**: Warnings (SSL expiry, configuration issues)
- **Red**: Errors, failures, down endpoints

### Shutdown Summary

Press `Ctrl+C` to gracefully stop monitoring. A summary displays:
- Total monitoring uptime
- Per-endpoint statistics:
  - Current status (UP/DOWN)
  - Uptime percentage
  - Successful/total checks
  - Consecutive failures
  - SSL certificate expiry

## Technical Details

| Setting | Value |
|---------|-------|
| HTTP Timeout | 30 seconds |
| Max Backoff | 5 minutes |
| Backoff Multiplier | 2x |
| SSL Warning Threshold | 30 days |
| Dashboard Refresh | 5 seconds |
| Default Check Interval | 10 seconds |

## Platform Support

- **Full support**: Windows (including sound alerts and window hiding)
- **Partial support**: Linux/macOS (sound alerts and window hiding unavailable)

## License

MIT License
