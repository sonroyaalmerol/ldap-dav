package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/sonroyaalmerol/ldap-dav/internal/cache"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
)

type OAuthState struct {
	State        string
	CodeVerifier string
	RedirectURI  string
	ExpiresAt    time.Time
}

type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
}

type OAuthSession struct {
	Principal    *Principal
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	CreatedAt    time.Time
	mu           sync.RWMutex
}

type OAuthAuth struct {
	cfg          *config.Config
	Dir          directory.Directory
	Logger       zerolog.Logger
	stateCache   *cache.Cache[string, *OAuthState]
	sessionCache *cache.Cache[string, *OAuthSession]
	refreshLocks sync.Map // map[sessionID]*sync.Mutex
}

func NewOAuthAuth(cfg *config.Config, dir directory.Directory, logger zerolog.Logger) *OAuthAuth {
	return &OAuthAuth{
		cfg:          cfg,
		Dir:          dir,
		Logger:       logger,
		stateCache:   cache.New[string, *OAuthState](10 * time.Minute),
		sessionCache: cache.New[string, *OAuthSession](time.Duration(cfg.Auth.OAuthSessionTTL) * time.Second),
	}
}

// GenerateRandomString generates a cryptographically secure random string
func generateRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// GeneratePKCEChallenge creates a code challenge from a verifier
func generatePKCEChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// InitiateOAuthFlow starts the OAuth 2.0 authorization code flow with PKCE
func (o *OAuthAuth) InitiateOAuthFlow(w http.ResponseWriter, req *http.Request) {
	state, err := generateRandomString(32)
	if err != nil {
		o.Logger.Error().Err(err).Msg("failed to generate state")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	codeVerifier, err := generateRandomString(43) // 43-128 chars for PKCE
	if err != nil {
		o.Logger.Error().Err(err).Msg("failed to generate code verifier")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	codeChallenge := generatePKCEChallenge(codeVerifier)

	// Store state with verifier for callback validation
	o.stateCache.SetWithExpiry(state, &OAuthState{
		State:        state,
		CodeVerifier: codeVerifier,
		RedirectURI:  o.cfg.Auth.OAuthRedirectURI,
		ExpiresAt:    time.Now().Add(10 * time.Minute),
	}, time.Now().Add(10*time.Minute))

	// Build authorization URL
	params := url.Values{}
	params.Set("client_id", o.cfg.Auth.OAuthClientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", o.cfg.Auth.OAuthRedirectURI)
	params.Set("state", state)
	params.Set("scope", o.cfg.Auth.OAuthScope)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")

	// Request offline_access for refresh tokens
	if o.cfg.Auth.OAuthEnableRefresh {
		currentScope := params.Get("scope")
		if !strings.Contains(currentScope, "offline_access") {
			params.Set("scope", currentScope+" offline_access")
		}
	}

	authURL := fmt.Sprintf("%s?%s", o.cfg.Auth.OAuthAuthURL, params.Encode())

	http.Redirect(w, req, authURL, http.StatusFound)
}

// HandleOAuthCallback processes the OAuth callback and exchanges code for token
func (o *OAuthAuth) HandleOAuthCallback(w http.ResponseWriter, req *http.Request) {
	state := req.URL.Query().Get("state")
	code := req.URL.Query().Get("code")
	errParam := req.URL.Query().Get("error")

	if errParam != "" {
		o.Logger.Error().Str("error", errParam).Msg("oauth error from provider")
		http.Error(w, fmt.Sprintf("OAuth error: %s", errParam), http.StatusBadRequest)
		return
	}

	if state == "" || code == "" {
		http.Error(w, "missing state or code", http.StatusBadRequest)
		return
	}

	// Validate state
	oauthState, ok := o.stateCache.Get(state)
	if !ok {
		o.Logger.Warn().Str("state", state).Msg("invalid or expired state")
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	if time.Now().After(oauthState.ExpiresAt) {
		http.Error(w, "state expired", http.StatusBadRequest)
		return
	}

	// Exchange code for token
	tokenResp, err := o.exchangeCodeForToken(code, oauthState.CodeVerifier)
	if err != nil {
		o.Logger.Error().Err(err).Msg("failed to exchange code for token")
		http.Error(w, "token exchange failed", http.StatusInternalServerError)
		return
	}

	// Validate token and extract user info
	principal, err := o.validateAndExtractPrincipal(req.Context(), tokenResp.AccessToken)
	if err != nil {
		o.Logger.Error().Err(err).Msg("failed to validate token")
		http.Error(w, "token validation failed", http.StatusUnauthorized)
		return
	}

	// Create session with token info
	sessionID, err := generateRandomString(32)
	if err != nil {
		o.Logger.Error().Err(err).Msg("failed to generate session ID")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	expiresIn := tokenResp.ExpiresIn
	if expiresIn == 0 {
		expiresIn = 3600 // default 1 hour
	}

	session := &OAuthSession{
		Principal:    principal,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
		CreatedAt:    time.Now(),
	}

	ttl := time.Duration(o.cfg.Auth.OAuthSessionTTL) * time.Second
	o.sessionCache.SetWithExpiry(sessionID, session, time.Now().Add(ttl))

	o.Logger.Info().
		Str("user", principal.UserID).
		Str("session", sessionID).
		Bool("has_refresh_token", tokenResp.RefreshToken != "").
		Time("expires_at", session.ExpiresAt).
		Msg("oauth session created")

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "dav_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   o.cfg.Auth.OAuthSecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   o.cfg.Auth.OAuthSessionTTL,
	})

	// Redirect to DAV base path
	http.Redirect(w, req, o.cfg.HTTP.BasePath, http.StatusFound)
}

// exchangeCodeForToken exchanges authorization code for access token
func (o *OAuthAuth) exchangeCodeForToken(code, codeVerifier string) (*OAuthTokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", o.cfg.Auth.OAuthRedirectURI)
	data.Set("client_id", o.cfg.Auth.OAuthClientID)
	data.Set("code_verifier", codeVerifier)

	if o.cfg.Auth.OAuthClientSecret != "" {
		data.Set("client_secret", o.cfg.Auth.OAuthClientSecret)
	}

	return o.requestToken(data)
}

// refreshAccessToken uses refresh token to get a new access token
func (o *OAuthAuth) refreshAccessToken(refreshToken string) (*OAuthTokenResponse, error) {
	if refreshToken == "" {
		return nil, errors.New("no refresh token available")
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", o.cfg.Auth.OAuthClientID)

	if o.cfg.Auth.OAuthClientSecret != "" {
		data.Set("client_secret", o.cfg.Auth.OAuthClientSecret)
	}

	return o.requestToken(data)
}

// requestToken is a helper to make token requests
func (o *OAuthAuth) requestToken(data url.Values) (*OAuthTokenResponse, error) {
	req, err := http.NewRequest(http.MethodPost, o.cfg.Auth.OAuthTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp OAuthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}

	return &tokenResp, nil
}

// validateAndExtractPrincipal validates the access token and extracts user info
func (o *OAuthAuth) validateAndExtractPrincipal(ctx context.Context, accessToken string) (*Principal, error) {
	// If userinfo endpoint is configured, use it
	if o.cfg.Auth.OAuthUserInfoURL != "" {
		return o.fetchUserInfo(ctx, accessToken)
	}

	// Otherwise, fall back to introspection or JWT validation
	if o.cfg.Auth.IntrospectURL != "" {
		valid, sub, err := o.Dir.IntrospectToken(ctx, accessToken, o.cfg.Auth.IntrospectURL, o.cfg.Auth.IntrospectAuthHeader)
		if err != nil || !valid {
			return nil, errors.New("invalid token")
		}

		user, err := o.Dir.LookupUserByAttr(ctx, o.cfg.LDAP.TokenUserAttr, sub)
		if err != nil {
			return nil, err
		}

		return &Principal{
			UserID:  user.UID,
			UserDN:  user.DN,
			Display: user.DisplayName,
		}, nil
	}

	return nil, errors.New("no user info endpoint configured")
}

// fetchUserInfo retrieves user information from OAuth userinfo endpoint
func (o *OAuthAuth) fetchUserInfo(ctx context.Context, accessToken string) (*Principal, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.cfg.Auth.OAuthUserInfoURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo returned %d", resp.StatusCode)
	}

	var userInfo map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, err
	}

	// Extract subject/username
	sub, ok := userInfo[o.cfg.Auth.OAuthSubjectClaim].(string)
	if !ok || sub == "" {
		sub, ok = userInfo["sub"].(string)
		if !ok || sub == "" {
			return nil, errors.New("no subject in userinfo")
		}
	}

	// Look up user in directory
	user, err := o.Dir.LookupUserByAttr(ctx, o.cfg.LDAP.TokenUserAttr, sub)
	if err != nil {
		return nil, err
	}

	return &Principal{
		UserID:  user.UID,
		UserDN:  user.DN,
		Display: user.DisplayName,
	}, nil
}

// AuthenticateSession validates a session cookie and refreshes token if needed
func (o *OAuthAuth) AuthenticateSession(ctx context.Context, sessionID string) (*Principal, error) {
	session, ok := o.sessionCache.Get(sessionID)
	if !ok {
		return nil, errors.New("invalid session")
	}

	// Check if token needs refresh
	if o.cfg.Auth.OAuthEnableRefresh && o.shouldRefresh(session) {
		if err := o.refreshSession(ctx, sessionID, session); err != nil {
			o.Logger.Warn().
				Err(err).
				Str("session", sessionID).
				Str("user", session.Principal.UserID).
				Msg("failed to refresh token")

			// If refresh fails, return error to force re-authentication
			if time.Now().After(session.ExpiresAt) {
				o.sessionCache.Delete(sessionID)
				return nil, errors.New("session expired and refresh failed")
			}
		}
	}

	session.mu.RLock()
	defer session.mu.RUnlock()

	return session.Principal, nil
}

// shouldRefresh determines if a token should be refreshed
func (o *OAuthAuth) shouldRefresh(session *OAuthSession) bool {
	session.mu.RLock()
	defer session.mu.RUnlock()

	if session.RefreshToken == "" {
		return false
	}

	// Refresh if token expires in less than configured threshold
	threshold := time.Duration(o.cfg.Auth.OAuthRefreshThreshold) * time.Second
	return time.Until(session.ExpiresAt) < threshold
}

// refreshSession refreshes the access token for a session
func (o *OAuthAuth) refreshSession(ctx context.Context, sessionID string, session *OAuthSession) error {
	// Get or create a lock for this session to prevent concurrent refreshes
	lockVal, _ := o.refreshLocks.LoadOrStore(sessionID, &sync.Mutex{})
	lock := lockVal.(*sync.Mutex)

	lock.Lock()
	defer lock.Unlock()

	// Re-check if we still need to refresh (another goroutine might have done it)
	if !o.shouldRefresh(session) {
		return nil
	}

	session.mu.RLock()
	refreshToken := session.RefreshToken
	session.mu.RUnlock()

	o.Logger.Debug().
		Str("session", sessionID).
		Str("user", session.Principal.UserID).
		Msg("refreshing access token")

	tokenResp, err := o.refreshAccessToken(refreshToken)
	if err != nil {
		return fmt.Errorf("refresh token request failed: %w", err)
	}

	// Validate new token
	principal, err := o.validateAndExtractPrincipal(ctx, tokenResp.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to validate refreshed token: %w", err)
	}

	// Update session with new tokens
	session.mu.Lock()
	session.AccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		// Some providers issue a new refresh token
		session.RefreshToken = tokenResp.RefreshToken
	}
	session.Principal = principal

	expiresIn := tokenResp.ExpiresIn
	if expiresIn == 0 {
		expiresIn = 3600
	}
	session.ExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	session.mu.Unlock()

	// Update cache with extended TTL
	ttl := time.Duration(o.cfg.Auth.OAuthSessionTTL) * time.Second
	o.sessionCache.SetWithExpiry(sessionID, session, time.Now().Add(ttl))

	o.Logger.Info().
		Str("session", sessionID).
		Str("user", principal.UserID).
		Time("new_expires_at", session.ExpiresAt).
		Msg("access token refreshed")

	return nil
}

// GetAccessToken returns the current access token for a session (useful for proxying)
func (o *OAuthAuth) GetAccessToken(sessionID string) (string, error) {
	session, ok := o.sessionCache.Get(sessionID)
	if !ok {
		return "", errors.New("invalid session")
	}

	session.mu.RLock()
	defer session.mu.RUnlock()

	return session.AccessToken, nil
}

// Logout invalidates a session
func (o *OAuthAuth) Logout(sessionID string) {
	o.sessionCache.Delete(sessionID)
	o.refreshLocks.Delete(sessionID)
}
