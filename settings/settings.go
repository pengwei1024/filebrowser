package settings

import (
	"crypto/rand"
	"io/fs"
	"log"
	"strings"
	"time"

	"github.com/filebrowser/filebrowser/v2/rules"
)

const DefaultUsersHomeBasePath = "/users"
const DefaultLogoutPage = "/login"
const DefaultMinimumPasswordLength = 12
const DefaultFileMode = 0640
const DefaultDirMode = 0750

// AuthMethod describes an authentication method.
type AuthMethod string

// Settings contain the main settings of the application.
type Settings struct {
	Key                   []byte              `json:"key"`
	Signup                bool                `json:"signup"`
	HideLoginButton       bool                `json:"hideLoginButton"`
	CreateUserDir         bool                `json:"createUserDir"`
	UserHomeBasePath      string              `json:"userHomeBasePath"`
	Defaults              UserDefaults        `json:"defaults"`
	AuthMethod            AuthMethod          `json:"authMethod"`
	LogoutPage            string              `json:"logoutPage"`
	Branding              Branding            `json:"branding"`
	Tus                   Tus                 `json:"tus"`
	Commands              map[string][]string `json:"commands"`
	Shell                 []string            `json:"shell"`
	Rules                 []rules.Rule        `json:"rules"`
	MinimumPasswordLength uint                `json:"minimumPasswordLength"`
	FileMode              fs.FileMode         `json:"fileMode"`
	DirMode               fs.FileMode         `json:"dirMode"`
	HideDotfiles          bool                `json:"hideDotfiles"`

	// LoginAlertToken and LoginAlertGroup configure the Baidu IM webhook that is
	// notified when the login throttle flags a suspected brute-force attack.
	// They are edited through the web admin Settings page; when either is empty
	// or zero no alert is sent, so the feature is off by default.
	LoginAlertToken string `json:"loginAlertToken"`
	LoginAlertGroup int    `json:"loginAlertGroup"`
}

// GetRules implements rules.Provider.
func (s *Settings) GetRules() []rules.Rule {
	return s.Rules
}

// Server specific settings.
type Server struct {
	Root                   string `json:"root"`
	BaseURL                string `json:"baseURL"`
	Socket                 string `json:"socket"`
	TLSKey                 string `json:"tlsKey"`
	TLSCert                string `json:"tlsCert"`
	Port                   string `json:"port"`
	Address                string `json:"address"`
	Log                    string `json:"log"`
	EnableThumbnails       bool   `json:"enableThumbnails"`
	ResizePreview          bool   `json:"resizePreview"`
	EnableExec             bool   `json:"enableExec"`
	TypeDetectionByHeader  bool   `json:"typeDetectionByHeader"`
	ImageResolutionCal     bool   `json:"imageResolutionCalculation"`
	AuthHook               string `json:"authHook"`
	TokenExpirationTime    string `json:"tokenExpirationTime"`
	FollowExternalSymlinks bool   `json:"followExternalSymlinks"`

	// LoginRateLimit and LoginRateLimitPerIP cap failed login attempts per
	// minute, keyed respectively by username and by client IP, to throttle
	// online password guessing. Values <= 0 fall back to conservative
	// defaults (see http.newRateLimiter). The throttle is always on; these
	// only tune its thresholds.
	LoginRateLimit      int `json:"loginRateLimit"`
	LoginRateLimitPerIP int `json:"loginRateLimitPerIP"`

	// CaseInsensitiveFs is detected from Root at startup rather than
	// configured, and tells the rule checker to match paths case-insensitively.
	// It is never persisted.
	CaseInsensitiveFs bool `json:"-"`
}

// Clean cleans any variables that might need cleaning.
func (s *Server) Clean() {
	s.BaseURL = strings.TrimSuffix(s.BaseURL, "/")
}

func (s *Server) GetTokenExpirationTime(fallback time.Duration) time.Duration {
	if s.TokenExpirationTime == "" {
		return fallback
	}

	duration, err := time.ParseDuration(s.TokenExpirationTime)
	if err != nil {
		log.Printf("[WARN] Failed to parse tokenExpirationTime: %v", err)
		return fallback
	}
	return duration
}

// GenerateKey generates a key of 512 bits.
func GenerateKey() ([]byte, error) {
	b := make([]byte, 64)
	_, err := rand.Read(b)
	// Note that err == nil only if we read len(b) bytes.
	if err != nil {
		return nil, err
	}

	return b, nil
}
