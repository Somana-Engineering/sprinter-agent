package services

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	"sprinter-agent/internal/config"
	"sprinter-agent/internal/generated"
)

// HostRegistrationService handles registration with main Somana instance
type HostRegistrationService struct {
	config   *config.Config
	client   *generated.ClientWithResponses
	hostRid  string
	stopChan chan bool
}

// NewHostRegistrationService creates a new host registration service
func NewHostRegistrationService(cfg *config.Config) *HostRegistrationService {
	log.Printf("Creating host registration service with URL: %s", cfg.HostRegistration.SprinterURL)
	
	httpClient := &http.Client{Timeout: 10 * time.Second}
	apiClient, err := generated.NewClientWithResponses(cfg.HostRegistration.SprinterURL, generated.WithHTTPClient(httpClient))
	if err != nil {
		log.Printf("Warning: failed to create client: %v", err)
	} else {
		log.Printf("Successfully created API client")
	}

	return &HostRegistrationService{
		config:   cfg,
		client:   apiClient,
		stopChan: make(chan bool),
	}
}

// Start begins the host registration and heartbeat process
func (s *HostRegistrationService) Start() error {
	if s.config.HostRegistration.SprinterURL == "" {
		log.Println("Host registration not configured - skipping")
		return nil
	}

	// Get system information
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to get hostname: %w", err)
	}

	ipAddress, err := s.getIP()
	if err != nil {
		return fmt.Errorf("failed to get IP address: %w", err)
	}

	osVersion, err := s.getOSVersion()
	if err != nil {
		log.Printf("Warning: failed to get OS version: %v", err)
		osVersion = "Unknown"
	}

	// Start registration retry loop in a goroutine
	go s.registrationLoop(hostname, ipAddress, osVersion)

	log.Println("Host registration service started (retrying until successful)")
	return nil
}

// registrationLoop continuously retries host registration until successful
func (s *HostRegistrationService) registrationLoop(hostname, ipAddress, osVersion string) {
	retryDelay := 5 * time.Second
	maxRetryDelay := 5 * time.Minute

	for {
		select {
		case <-s.stopChan:
			return
		default:
			// Try to register
			err := s.registerHost(hostname, ipAddress, osVersion)
			if err == nil {
				// Registration successful
				log.Printf("Host registration successful - Host RID: %s", s.hostRid)
				
				// Start heartbeat goroutine
				go s.startHeartbeat()
				return
			}

			// Registration failed, log and retry
			log.Printf("Host registration failed: %v. Retrying in %v...", err, retryDelay)
			time.Sleep(retryDelay)

			// Exponential backoff with max limit
			retryDelay = retryDelay * 2
			if retryDelay > maxRetryDelay {
				retryDelay = maxRetryDelay
			}
		}
	}
}

// GetHostRid returns the host RID
func (s *HostRegistrationService) GetHostRid() string {
	return s.hostRid
}

// GetClient returns the API client
func (s *HostRegistrationService) GetClient() *generated.ClientWithResponses {
	return s.client
}

// Stop stops the heartbeat process
func (s *HostRegistrationService) Stop() {
	if s.config.HostRegistration.SprinterURL != "" {
		close(s.stopChan)
		log.Println("Host registration stopped")
	}
}

// registerHost registers this host with the main Somana instance
func (s *HostRegistrationService) registerHost(hostname, ipAddress, osVersion string) error {
	ctx := context.Background()

	log.Printf("Attempting to register host: %s (%s) - %s", hostname, ipAddress, osVersion)

	// Check if we have a host RID stored on disk
	hostRid, err := s.loadHostRid()
	if err != nil {
		log.Printf("Failed to load host RID from disk: %v", err)
	}

	// If RID exists on disk, verify it exists on the server
	if hostRid != "" {
		log.Printf("Found host RID on disk: %s, verifying with server", hostRid)
		
		// Check if host exists with this RID
		resp, err := s.client.GetApiV1HostsHostRidWithResponse(ctx, generated.HostRid(hostRid))
		if err != nil {
			log.Printf("Failed to check host existence: %v", err)
			return fmt.Errorf("failed to check host existence: %w", err)
		}

		if resp.StatusCode() == http.StatusOK && resp.JSON200 != nil {
			// Host exists with this RID, use it
			s.hostRid = hostRid
			log.Printf("Verified existing host with RID: %s", s.hostRid)
			
			// Update host information in case it changed
			if err := s.updateHost(hostname, ipAddress); err != nil {
				log.Printf("Warning: failed to update host information: %v", err)
			}
			
			return nil
		} else {
			log.Printf("Host with RID %s does not exist on server, will create new host", hostRid)
			// RID exists on disk but not on server - create new host with this RID
		}
	}

	// Generate new RID if we don't have one
	if hostRid == "" {
		hostRid = s.generateHostRid()
		log.Printf("Generated new host RID: %s", hostRid)
	}

	// Set the RID we'll use
	s.hostRid = hostRid
	
	// Validate RID is set
	if s.hostRid == "" {
		return fmt.Errorf("host RID is empty after generation - this should not happen")
	}
	
	log.Printf("Using host RID for registration: %s", s.hostRid)

	// Get OS name from runtime
	osName := runtime.GOOS
	if osName == "darwin" {
		osName = "macOS"
	}

	// Register new host
	reqBody := generated.HostCreateRequest{
		HostRid:   generated.HostRid(s.hostRid),
		Hostname:  hostname,
		IpAddress: ipAddress,
		OsName:    osName,
		OsVersion: osVersion,
	}

	log.Printf("Sending registration request to: %s/api/v1/hosts", s.config.HostRegistration.SprinterURL)
	resp, err := s.client.PostApiV1HostsWithResponse(ctx, reqBody)
	if err != nil {
		log.Printf("Registration request failed: %v", err)
		return fmt.Errorf("failed to register host: %w", err)
	}

	log.Printf("Registration response status: %d", resp.StatusCode())
	if resp.StatusCode() != http.StatusCreated {
		log.Printf("Registration failed with status: %d", resp.StatusCode())
		return fmt.Errorf("registration failed with status: %d", resp.StatusCode())
	}

	if resp.JSON201 == nil {
		log.Printf("No host data in response")
		return fmt.Errorf("no host data in response")
	}

	// Use the RID from the server response (server may have validated/modified it)
	serverRid := string(resp.JSON201.HostRid)
	if serverRid == "" {
		log.Printf("Warning: Server returned empty RID, using locally generated RID: %s", s.hostRid)
	} else if serverRid != s.hostRid {
		log.Printf("Server returned different RID (%s) than we sent (%s), using server RID", serverRid, s.hostRid)
		s.hostRid = serverRid
	}

	// Save the RID to disk
	if err := s.saveHostRid(s.hostRid); err != nil {
		log.Printf("Warning: failed to save host RID to disk: %v", err)
	}

	log.Printf("Successfully registered host with RID: %s", s.hostRid)
	return nil
}

// updateHost updates host information on the server
func (s *HostRegistrationService) updateHost(hostname, ipAddress string) error {
	ctx := context.Background()

	reqBody := generated.HostUpdateRequest{
		Hostname:  &hostname,
		IpAddress: &ipAddress,
	}

	resp, err := s.client.PutApiV1HostsHostRidWithResponse(ctx, generated.HostRid(s.hostRid), reqBody)
	if err != nil {
		return fmt.Errorf("failed to update host: %w", err)
	}

	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("update failed with status: %d", resp.StatusCode())
	}

	return nil
}

// generateHostRid generates a new UUID-based RID
func (s *HostRegistrationService) generateHostRid() string {
	return uuid.New().String()
}

// getRidFilePath returns the path to the RID storage file
func (s *HostRegistrationService) getRidFilePath() string {
	return filepath.Join("data", "host.rid")
}

// loadHostRid loads the host RID from disk
func (s *HostRegistrationService) loadHostRid() (string, error) {
	ridPath := s.getRidFilePath()
	
	data, err := os.ReadFile(ridPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // File doesn't exist, no RID stored
		}
		return "", fmt.Errorf("failed to read RID file: %w", err)
	}

	rid := strings.TrimSpace(string(data))
	if rid == "" {
		return "", nil
	}

	return rid, nil
}

// saveHostRid saves the host RID to disk
func (s *HostRegistrationService) saveHostRid(rid string) error {
	ridPath := s.getRidFilePath()
	
	// Ensure directory exists
	dir := filepath.Dir(ridPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Write RID to file
	if err := os.WriteFile(ridPath, []byte(rid), 0644); err != nil {
		return fmt.Errorf("failed to write RID file: %w", err)
	}

	return nil
}

// startHeartbeat starts the heartbeat process
func (s *HostRegistrationService) startHeartbeat() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Send initial heartbeat immediately
	if err := s.sendHeartbeat(); err != nil {
		log.Printf("Failed to send initial heartbeat: %v", err)
	} else {
		log.Printf("Heartbeat sent successfully")
	}

	for {
		select {
		case <-ticker.C:
			if err := s.sendHeartbeat(); err != nil {
				log.Printf("Failed to send heartbeat: %v (will retry on next interval)", err)
			} else {
				log.Printf("Heartbeat sent successfully")
			}
		case <-s.stopChan:
			return
		}
	}
}

// sendHeartbeat sends a heartbeat to the main Somana instance
func (s *HostRegistrationService) sendHeartbeat() error {
	if s.hostRid == "" {
		return fmt.Errorf("host RID not set, skipping heartbeat")
	}

	ctx := context.Background()
	
	// API changed: status field removed, server tracks last_heartbeat automatically
	reqBody := generated.HostHeartbeatRequest{}

	resp, err := s.client.PostApiV1HostsHostRidHeartbeatWithResponse(ctx, generated.HostRid(s.hostRid), reqBody)
	if err != nil {
		return fmt.Errorf("failed to send heartbeat: %w", err)
	}

	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("heartbeat failed with status: %d", resp.StatusCode())
	}

	return nil
}

// getIP gets the IP address, preferring Tailscale IP if available
func (s *HostRegistrationService) getIP() (string, error) {
	// Try to get IP from tailscale first
	if output, err := exec.Command("tailscale", "ip").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		if len(lines) > 0 && lines[0] != "" {
			ip := strings.TrimSpace(lines[0])
			// Validate it's a valid IP address
			if parsedIP := net.ParseIP(ip); parsedIP != nil {
				log.Printf("Using Tailscale IP: %s", ip)
				return ip, nil
			}
		}
	}

	// Fallback to original method if tailscale is not available
	log.Printf("tailscale ip command not available, falling back to hostname lookup")
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}

	addrs, err := net.LookupIP(hostname)
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		if !addr.IsLoopback() && addr.To4() != nil {
			return addr.String(), nil
		}
	}

	return "127.0.0.1", nil
}

// getOSVersion gets the OS version information
func (s *HostRegistrationService) getOSVersion() (string, error) {
	switch runtime.GOOS {
	case "linux":
		// Try to read /etc/os-release
		if data, err := os.ReadFile("/etc/os-release"); err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "PRETTY_NAME=") {
					version := strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
					return version, nil
				}
			}
		}

		// Fallback to uname
		if output, err := exec.Command("uname", "-r").Output(); err == nil {
			return "Linux " + strings.TrimSpace(string(output)), nil
		}

		return "Linux", nil
	case "darwin":
		if output, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
			return "macOS " + strings.TrimSpace(string(output)), nil
		}
		return "macOS", nil
	default:
		return runtime.GOOS, nil
	}
} 