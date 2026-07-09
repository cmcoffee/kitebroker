package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cmcoffee/snugforge/nfo"
)

// maxDeliveryBytes caps the size of a single webhook delivery body.
const maxDeliveryBytes = 8 << 20 // 8 MB

// WebhookListenerConfig holds the shared infrastructure settings for the
// webhook listener. These are operator/appliance concerns (where to listen,
// how to authenticate deliveries) and are configured once via --setup, not
// per webhook task.
type WebhookListenerConfig struct {
	Enabled      bool   // Whether webhook delivery is enabled; when false, tasks fall back to polling.
	Scheme       string // "https" (default) or "http"; http is for use behind a TLS-terminating proxy.
	Bind         string // Address and port to listen on, e.g. "0.0.0.0:8080".
	Path         string // URL path that receives deliveries, e.g. "/webhook".
	Secret       string // Shared secret for HMAC-SHA256 signature verification.
	Token        string // Shared token expected on each delivery.
	SigHeader    string // Header carrying the HMAC signature.
	TokenHeader  string // Header carrying the shared token.
	Workers      int    // Max deliveries handled concurrently.
	TLSCert      string // TLS certificate file (optional).
	TLSKey       string // TLS private key file (optional).
	SelfRegister bool   // Register/unregister with the appliance automatically.
	PublicURL    string // Public URL the appliance should deliver to.
}

// webhookListener is the process-wide singleton that owns the HTTP server and
// routes deliveries to hosted webhook tasks via the handler registry.
type webhookListener struct {
	mu         sync.Mutex
	cfg        WebhookListenerConfig
	configured bool
	started    bool
	srv        *http.Server
	done       chan struct{} // closed when the listener has shut down.
	doneOnce   sync.Once
	limiter    LimitGroup
	subjects        map[string]struct{} // union of hosted tasks' subjects, for self-register
	selfHookID      string
	selfHookAdopted bool // true when we reused a pre-existing webhook (do not delete on exit)
	selfKW          KWSession
	warnUnauth sync.Once

	report     *TaskReport
	received   Tally
	rejected   Tally
	dispatched Tally
	unmatched  Tally
	handlerErr Tally
}

var listener = &webhookListener{subjects: make(map[string]struct{}), done: make(chan struct{})}

// ConfigureWebhookListener installs the shared listener configuration. It must
// be called once (typically from main, populated from --setup) before any
// webhook task is hosted. Sensible defaults are applied for blank fields.
func ConfigureWebhookListener(cfg WebhookListenerConfig) error {
	cfg.Scheme = strings.ToLower(strings.TrimSpace(cfg.Scheme))
	if IsBlank(cfg.Scheme) {
		cfg.Scheme = "https"
	}
	switch cfg.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("unsupported webhook scheme %q; use http or https", cfg.Scheme)
	}
	if IsBlank(cfg.Bind) {
		cfg.Bind = "0.0.0.0:8080"
	}
	if IsBlank(cfg.Path) {
		cfg.Path = "/webhook"
	}
	if IsBlank(cfg.Path) || cfg.Path[0] != '/' {
		return fmt.Errorf("webhook listener path must begin with '/'")
	}
	if IsBlank(cfg.SigHeader) {
		cfg.SigHeader = "X-KW-Signature"
	}
	if IsBlank(cfg.TokenHeader) {
		cfg.TokenHeader = "Authorization"
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 16
	}
	if (IsBlank(cfg.TLSCert)) != (IsBlank(cfg.TLSKey)) {
		return fmt.Errorf("webhook tls_cert and tls_key must be provided together")
	}
	if cfg.SelfRegister && IsBlank(cfg.PublicURL) {
		return fmt.Errorf("webhook self_register requires a public_url")
	}

	listener.mu.Lock()
	defer listener.mu.Unlock()
	listener.cfg = cfg
	listener.configured = true
	return nil
}

// WebhookListenerEnabled reports whether the webhook listener is configured and
// enabled. Tasks that can operate by either webhook or polling use this to pick
// their transport.
func WebhookListenerEnabled() bool {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	return listener.configured && listener.cfg.Enabled
}

// WebhookListenerStarted reports whether the HTTP listener has actually been
// started (i.e. a task is being hosted in the background). The menu uses this,
// after a task's Main returns, to decide whether the task is now listening
// rather than finished.
func WebhookListenerStarted() bool {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	return listener.started
}

// HostWebhookTask registers a webhook task with the shared listener and starts
// the HTTP server on first use. It does NOT block: the task's Main can return
// immediately after calling this. The foreground is kept alive afterwards by
// WaitForWebhookListener (called from main), which blocks until Ctrl+C.
//
// kw is the session deliveries for this task's subjects are dispatched with.
func HostWebhookTask(task WebhookTask, kw KWSession) error {
	listener.mu.Lock()
	if !listener.configured {
		listener.mu.Unlock()
		return fmt.Errorf("webhook listener is not configured; run --setup to configure the PubSub listener")
	}

	subjects := task.Subjects()
	if len(subjects) == 0 {
		listener.mu.Unlock()
		return fmt.Errorf("webhook task %q declares no subjects", task.Name())
	}

	// Tallies recorded by a webhook task's Handle must survive past its Main
	// returning, so point the task's report at the listener's shared report
	// (printed on shutdown) instead of the per-run report the menu discards.
	listener.ensureReport()
	task.Get().Report = listener.report

	// Subjects() are appliance subscription patterns (e.g. file_folder_modify.>)
	// used for self-registration. They are a different namespace from the
	// event_name carried on a delivery (e.g. add_file_version), so we do NOT use
	// them as dispatch keys. The appliance already filters deliveries to our
	// subscriptions, so each hosted task receives every delivery via a catch-all
	// handler and filters by event in its Handle.
	for _, subject := range subjects {
		listener.subjects[subject] = struct{}{}
	}
	t := task
	RegisterWebhookHandler(SubjectAll, func(ev WebhookEvent, _ KWSession) error {
		return t.Handle(ev)
	})
	Log("Hosting webhook task %q (subscriptions: %s).", task.Name(), strings.Join(subjects, ", "))

	// Remember a session to drive self-register/unregister.
	if IsBlank(listener.selfKW.Username) {
		listener.selfKW = kw
	}

	start := !listener.started
	listener.started = true
	listener.mu.Unlock()

	if start {
		return listener.start()
	}
	return nil
}

// ensureReport creates the listener's shared report and tallies once. The
// caller must hold listener.mu.
func (l *webhookListener) ensureReport() {
	if l.report != nil {
		return
	}
	l.report = NewTaskReport("pubsub_listener", "listener", nil)
	l.received = l.report.Tally("Deliveries Received")
	l.rejected = l.report.Tally("Deliveries Rejected (auth)")
	l.dispatched = l.report.Tally("Events Dispatched")
	l.unmatched = l.report.Tally("Events Unmatched")
	l.handlerErr = l.report.Tally("Handler Errors")
}

// start brings up the HTTP server, optional self-registration, and the
// shutdown hook. It is called once, under the assumption the caller set
// listener.started.
func (l *webhookListener) start() error {
	l.limiter = NewLimitGroup(l.cfg.Workers)

	// The shared report and its tallies are created in ensureReport when the
	// first task is hosted; deliveries are tallied against it even though no
	// task's Main is running while the server serves.
	l.mu.Lock()
	l.ensureReport()
	l.mu.Unlock()

	if l.cfg.SelfRegister {
		if err := l.selfRegister(); err != nil {
			return err
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc(l.cfg.Path, l.handleDelivery)
	l.srv = &http.Server{
		Addr:              l.cfg.Bind,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Unless explicitly configured for plaintext (http, e.g. behind a
	// TLS-terminating proxy), the listener serves over TLS. When no
	// certificate/key is configured, fall back to a generated self-signed
	// certificate so deliveries are never accepted over plaintext by default.
	serve_tls := l.cfg.Scheme != "http"
	cert_file, key_file := l.cfg.TLSCert, l.cfg.TLSKey
	if serve_tls && IsBlank(cert_file) {
		cert, err := l.selfSignedCert()
		if err != nil {
			return fmt.Errorf("could not create self-signed certificate: %w", err)
		}
		l.srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		Warn("No TLS certificate configured; using a generated self-signed certificate. Configure tls_cert/tls_key via --setup for a trusted certificate.")
	}

	// On SIGINT, stop accepting, drain in-flight deliveries, and clean up. The
	// callback returns false so the signal goroutine does NOT terminate the
	// process itself; instead it unblocks WaitForWebhookListener, letting main
	// drive a single, clean exit. (Returning true here, or relying on
	// BlockShutdown, deadlocks: a non-SIGINT exit would never drain.)
	nfo.SignalCallback(syscall.SIGINT, func() bool {
		Log("Shutdown requested; draining in-flight deliveries ...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		l.srv.Shutdown(ctx)
		l.limiter.Wait()
		l.selfUnregister()
		l.report.Summary(ErrCount())
		l.signalDone()
		return false
	})

	go func() {
		var err error
		if serve_tls {
			// cert_file/key_file are empty when serving with the in-memory
			// self-signed certificate set on TLSConfig above.
			err = l.srv.ListenAndServeTLS(cert_file, key_file)
		} else {
			err = l.srv.ListenAndServe()
		}
		// A clean Shutdown returns ErrServerClosed; anything else is a startup
		// or runtime failure that should also unblock the foreground.
		if err != nil && err != http.ErrServerClosed {
			Err("Webhook listener stopped: %v", err)
			l.signalDone()
		}
	}()

	scheme := "https"
	if !serve_tls {
		scheme = "http"
	}
	Log("Listening for webhook deliveries on %s://%s%s", scheme, l.cfg.Bind, l.cfg.Path)
	Log("Press Ctrl+C to stop the listener.")
	return nil
}

// signalDone closes the done channel exactly once, unblocking any caller of
// WaitForWebhookListener.
func (l *webhookListener) signalDone() {
	l.doneOnce.Do(func() { close(l.done) })
}

// WaitForWebhookListener blocks until the webhook listener has shut down. It
// returns immediately when no listener was ever started, so it is safe to call
// unconditionally after the task loop. While it blocks, the foreground stays
// alive and the signal handler remains armed to catch Ctrl+C.
func WaitForWebhookListener() {
	listener.mu.Lock()
	started := listener.started
	done := listener.done
	listener.mu.Unlock()
	if !started {
		return
	}
	<-done
}

// selfSignedCert returns a TLS certificate for the listener, generating a
// self-signed one when none has been cached yet. The generated certificate and
// key are cached under <root>/data so the same identity is reused across
// restarts (the self-registered delivery URL and any appliance trust stay
// stable). Subject Alternative Names include the public URL host (when
// configured), the bind host, and localhost.
func (l *webhookListener) selfSignedCert() (tls.Certificate, error) {
	dir := FormatPath(fmt.Sprintf("%s/data", MyRoot()))
	cert_path := FormatPath(fmt.Sprintf("%s/webhook_selfsigned.crt", dir))
	key_path := FormatPath(fmt.Sprintf("%s/webhook_selfsigned.key", dir))

	// Reuse a previously generated certificate when present.
	if cert, err := tls.LoadX509KeyPair(cert_path, key_path); err == nil {
		return cert, nil
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: l.certCommonName()},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range l.certHosts() {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	cert_pem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	key_der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	key_pem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: key_der})

	// Best-effort cache to disk; serving still proceeds if the write fails.
	if err := MkDir(dir); err == nil {
		if err := os.WriteFile(cert_path, cert_pem, 0644); err != nil {
			Debug("could not cache self-signed certificate: %v", err)
		}
		if err := os.WriteFile(key_path, key_pem, 0600); err != nil {
			Debug("could not cache self-signed key: %v", err)
		}
	}

	return tls.X509KeyPair(cert_pem, key_pem)
}

// certCommonName returns a CN for the self-signed certificate, preferring the
// public URL host.
func (l *webhookListener) certCommonName() string {
	if hosts := l.certHosts(); len(hosts) > 0 {
		return hosts[0]
	}
	return "kitebroker-webhook"
}

// certHosts returns the set of hostnames/IPs the self-signed certificate should
// be valid for: the public URL host (if set), the bind host, and localhost.
func (l *webhookListener) certHosts() []string {
	seen := make(map[string]struct{})
	var hosts []string
	add := func(h string) {
		h = strings.TrimSpace(h)
		if IsBlank(h) {
			return
		}
		if _, ok := seen[h]; ok {
			return
		}
		seen[h] = struct{}{}
		hosts = append(hosts, h)
	}

	if !IsBlank(l.cfg.PublicURL) {
		if u, err := url.Parse(l.cfg.PublicURL); err == nil {
			add(u.Hostname())
		}
	}
	if host, _, err := net.SplitHostPort(l.cfg.Bind); err == nil {
		// A bind host of 0.0.0.0/:: is a wildcard, not a usable SAN.
		if host != "0.0.0.0" && host != "::" {
			add(host)
		}
	}
	add("localhost")
	add("127.0.0.1")
	return hosts
}

// handleDelivery is the HTTP entry point for a single webhook delivery.
func (l *webhookListener) handleDelivery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDeliveryBytes))
	if err != nil {
		http.Error(w, "unable to read body", http.StatusBadRequest)
		return
	}

	// Dump the raw delivery for --snoop (TRACE), before verification so even
	// rejected deliveries are visible while diagnosing.
	Trace("--> webhook delivery from %s: %s %s", r.RemoteAddr, r.Method, r.URL.Path)
	for name, vals := range r.Header {
		Trace("    %s: %s", name, strings.Join(vals, ", "))
	}
	Trace("    body: %s", string(body))

	if !l.verify(body, r.Header) {
		l.rejected.Add(1)
		Err("Rejected webhook delivery from %s: signature/token verification failed.", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ev := bodyToEvent(body, r.Header)
	l.received.Add(1)

	// Acknowledge promptly so the appliance is not blocked on handler work;
	// dispatch runs asynchronously, bounded by the limiter.
	w.WriteHeader(http.StatusOK)

	l.limiter.Add(1)
	go func() {
		defer l.limiter.Done()
		matched, errs := DispatchWebhookEvent(ev, l.selfKW)
		if matched == 0 {
			l.unmatched.Add(1)
			return
		}
		l.dispatched.Add(1)
		for _, e := range errs {
			l.handlerErr.Add(1)
			Err("Handler error for subject %q: %v", ev.Subject, e)
		}
	}()
}

// verify checks the delivery against the configured secret and/or token. When
// neither is configured it accepts all deliveries and warns once.
func (l *webhookListener) verify(body []byte, hdr http.Header) bool {
	haveSecret := !IsBlank(l.cfg.Secret)
	haveToken := !IsBlank(l.cfg.Token)

	if !haveSecret && !haveToken {
		l.warnUnauth.Do(func() {
			Warn("Webhook listener is running unauthenticated; configure a secret and/or token via --setup to verify deliveries.")
		})
		return true
	}

	if haveSecret && !l.verifySignature(body, hdr) {
		return false
	}
	if haveToken && !l.verifyToken(hdr) {
		return false
	}
	return true
}

// verifySignature compares the HMAC-SHA256 of the body against the signature
// header, accepting either hex or base64 encoding. SHA256 is the only
// algorithm Kiteworks uses for webhook signatures.
func (l *webhookListener) verifySignature(body []byte, hdr http.Header) bool {
	provided := strings.TrimSpace(hdr.Get(l.cfg.SigHeader))
	if IsBlank(provided) {
		return false
	}
	// Tolerate a "sha256=" prefix.
	if eq := strings.Index(provided, "="); eq >= 0 && eq < 12 {
		provided = strings.TrimSpace(provided[eq+1:])
	}

	mac := hmac.New(sha256.New, []byte(l.cfg.Secret))
	mac.Write(body)
	sum := mac.Sum(nil)

	expectedHex := hex.EncodeToString(sum)
	expectedB64 := base64.StdEncoding.EncodeToString(sum)

	return constantTimeEqual(provided, expectedHex) || constantTimeEqual(provided, expectedB64)
}

// verifyToken compares the token header against the configured token,
// tolerating a "Bearer " prefix.
func (l *webhookListener) verifyToken(hdr http.Header) bool {
	provided := strings.TrimSpace(hdr.Get(l.cfg.TokenHeader))
	provided = strings.TrimPrefix(provided, "Bearer ")
	provided = strings.TrimSpace(provided)
	return constantTimeEqual(provided, l.cfg.Token)
}

// constantTimeEqual reports whether a and b are equal without leaking timing.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// selfRegister ensures a webhook registration exists on the appliance pointing
// at the configured public URL, subscribing to the union of hosted tasks'
// subjects. It is idempotent: if a webhook for the same URL already exists, it
// is updated in place (refreshing subscriptions, secret, token, and enabled
// state) rather than creating a duplicate. The secret and token are always
// sent so the appliance signs/authenticates deliveries with the credentials
// the listener verifies against.
func (l *webhookListener) selfRegister() error {
	subs := make([]string, 0, len(l.subjects))
	for s := range l.subjects {
		subs = append(subs, s)
	}
	if len(subs) == 0 {
		return fmt.Errorf("self_register: no subjects to subscribe to")
	}

	opts := PostJSON{"enabled": true}
	if !IsBlank(l.cfg.Secret) {
		opts["secret"] = l.cfg.Secret
	}
	if !IsBlank(l.cfg.Token) {
		opts["token"] = l.cfg.Token
	}

	// Look for an existing webhook with the same URL so we reuse it instead of
	// registering a duplicate (e.g. after a crash that skipped cleanup).
	existing, err := l.findWebhookByURL(l.cfg.PublicURL)
	if err != nil {
		return fmt.Errorf("self-register: %w", err)
	}
	if existing != nil {
		webhook, err := l.selfKW.Webhook(existing.ID).Update(l.cfg.PublicURL, subs, opts)
		if err != nil {
			return fmt.Errorf("self-register: updating existing webhook %s: %w", existing.ID, err)
		}
		// We adopted a pre-existing registration; leave it in place on shutdown.
		l.selfHookID = webhook.ID
		l.selfHookAdopted = true
		Log("Reusing existing webhook %s -> %s (updated subscriptions).", webhook.ID, webhook.URL)
		return nil
	}

	webhook, err := l.selfKW.CreateWebhook(l.cfg.PublicURL, subs, opts)
	if err != nil {
		return fmt.Errorf("self-register failed: %w", err)
	}
	l.selfHookID = webhook.ID
	l.selfHookAdopted = false
	Log("Registered webhook %s -> %s.", webhook.ID, webhook.URL)
	return nil
}

// findWebhookByURL returns the first existing webhook whose URL matches the
// given url (case-insensitive), or nil when none exists.
func (l *webhookListener) findWebhookByURL(url string) (*KiteWebhook, error) {
	webhooks, err := l.selfKW.Webhooks()
	if err != nil {
		return nil, err
	}
	for i := range webhooks {
		if strings.EqualFold(strings.TrimSpace(webhooks[i].URL), strings.TrimSpace(url)) {
			return &webhooks[i], nil
		}
	}
	return nil, nil
}

// selfUnregister removes the webhook created by selfRegister. A registration we
// adopted (one that already existed) is left in place.
func (l *webhookListener) selfUnregister() {
	if IsBlank(l.selfHookID) {
		return
	}
	if l.selfHookAdopted {
		Log("Leaving pre-existing webhook %s in place.", l.selfHookID)
		return
	}
	if err := l.selfKW.Webhook(l.selfHookID).Delete(); err != nil {
		Err("Failed to remove self-registered webhook %s: %v", l.selfHookID, err)
		return
	}
	Log("Removed self-registered webhook %s.", l.selfHookID)
}

// bodyToEvent parses a delivery body into a WebhookEvent. The Kiteworks PubSub
// delivery envelope looks like:
//
//	{
//	  "tenantId": "0",
//	  "webhookId": "c30f9ffb-...",
//	  "payload": {
//	    "event_name": "add_file_version",
//	    "created": 1781736809.709,        // float epoch seconds
//	    "data": { "file": {...}, "parent_folder": {...} },
//	    ...
//	  }
//	}
//
// The Subject is taken from payload.event_name. Data is set to the payload
// object so handlers can decode the rich fields (data.file, etc.). The raw body
// and headers are always preserved so handlers can re-decode anything.
func bodyToEvent(body []byte, hdr http.Header) WebhookEvent {
	ev := WebhookEvent{Raw: body, Header: hdr}

	var envelope struct {
		WebhookID string `json:"webhookId"`
		Payload   struct {
			EventName string          `json:"event_name"`
			Created   float64         `json:"created"`
			Data      json.RawMessage `json:"data"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		// Not JSON we recognize; leave Data as the whole body so handlers can
		// still inspect it.
		ev.Data = json.RawMessage(body)
		return ev
	}

	ev.WebhookID = envelope.WebhookID
	ev.Subject = strings.TrimSpace(envelope.Payload.EventName)
	ev.Event = ev.Subject

	if envelope.Payload.Created > 0 {
		sec := int64(envelope.Payload.Created)
		nsec := int64((envelope.Payload.Created - float64(sec)) * 1e9)
		ev.Timestamp = time.Unix(sec, nsec)
	}

	// The payload's data object carries the rich event fields. Fall back to the
	// whole payload, then the whole body, so handlers always have something.
	if len(envelope.Payload.Data) > 0 {
		ev.Data = envelope.Payload.Data
	} else {
		ev.Data = json.RawMessage(body)
	}

	return ev
}
