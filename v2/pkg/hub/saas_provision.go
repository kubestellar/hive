package hub

import (
	cryptoRand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"
)

const (
	saasHivesDir          = "/data/saas/hives"
	maxHivesPerUser       = 3
	maxSaaSHivesTotal     = 5
	provisionPollInterval = 30 * time.Second
	provisionTimeout      = 5 * time.Minute
	cpuRequest            = "200m"
	cpuLimit              = "500m"
	memRequest            = "256Mi"
	memLimit              = "512Mi"
	pvcSize               = "1Gi"
)

type SaaSHive struct {
	ID          string `json:"id"`
	Owner       string `json:"owner"`
	ProjectName string `json:"project_name"`
	Org         string `json:"org"`
	Repos       []string `json:"repos"`
	PrimaryRepo string `json:"primary_repo"`
	ACMMLevel   int    `json:"acmm_level"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	Subdomain   string `json:"subdomain"`
	Error       string `json:"error,omitempty"`
}

type CreateHiveRequest struct {
	Org            string `json:"org"`
	Repos          string `json:"repos"`
	PrimaryRepo    string `json:"primary_repo"`
	ProjectName    string `json:"project_name"`
	ACMMLevel      int    `json:"acmm_level"`
	GitHubToken    string `json:"github_token"`
	AuthMethod     string `json:"auth_method"`
	AppID          string `json:"app_id"`
	InstallationID string `json:"installation_id"`
	AppPrivateKey  string `json:"app_private_key"`
}

func generateHiveID(org, repo string) string {
	short := repo
	if len(short) > 12 {
		short = short[:12]
	}
	chars := "abcdefghijklmnopqrstuvwxyz0123456789"
	suffix := make([]byte, 4)
	for i := range suffix {
		suffix[i] = chars[rand.Intn(len(chars))]
	}
	return fmt.Sprintf("hosted-%s-%s-%s", sanitize(org), sanitize(short), string(suffix))
}

func sanitize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func loadSaaSHive(id string) *SaaSHive {
	if strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		return nil
	}
	path := filepath.Join(saasHivesDir, id, "meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var h SaaSHive
	if json.Unmarshal(data, &h) != nil {
		return nil
	}
	return &h
}

func saveSaaSHive(h *SaaSHive) error {
	dir := filepath.Join(saasHivesDir, h.ID)
	os.MkdirAll(dir, 0o755)
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "meta.json")
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func listSaaSHives() []SaaSHive {
	entries, err := os.ReadDir(saasHivesDir)
	if err != nil {
		return nil
	}
	var hives []SaaSHive
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		h := loadSaaSHive(e.Name())
		if h != nil {
			hives = append(hives, *h)
		}
	}
	return hives
}

func countUserHives(username string) int {
	count := 0
	for _, h := range listSaaSHives() {
		if h.Owner == username {
			count++
		}
	}
	return count
}

func provisionHive(h *SaaSHive, req *CreateHiveRequest, logger *slog.Logger) error {
	dir := filepath.Join(saasHivesDir, h.ID, "manifests")
	os.MkdirAll(dir, 0o755)

	repos := h.Repos
	reposYAML := "[]"
	if len(repos) > 0 {
		parts := make([]string, 0, len(repos))
		for _, r := range repos {
			clean := sanitize(r)
			if clean != "" {
				parts = append(parts, fmt.Sprintf("      - %s", clean))
			}
		}
		if len(parts) > 0 {
			reposYAML = "\n" + strings.Join(parts, "\n")
		}
	}

	useApp := req.AuthMethod == "app" && req.AppID != "" && req.InstallationID != "" && req.AppPrivateKey != ""

	data := map[string]any{
		"ID":             h.ID,
		"Namespace":      "hive-hosted-" + h.ID,
		"Org":            sanitize(h.Org),
		"Repos":          reposYAML,
		"PrimaryRepo":    sanitize(h.PrimaryRepo),
		"ACMMLevel":      h.ACMMLevel,
		"Token":          req.GitHubToken,
		"UseApp":         useApp,
		"AppID":          sanitize(req.AppID),
		"InstallationID": sanitize(req.InstallationID),
		"AppPrivateKey": func() string {
			lines := strings.Split(strings.TrimSpace(req.AppPrivateKey), "\n")
			for i := range lines {
				lines[i] = "    " + strings.TrimSpace(lines[i])
			}
			return strings.Join(lines, "\n")
		}(),
		"CPURequest":      cpuRequest,
		"CPULimit":        cpuLimit,
		"MemRequest":      memRequest,
		"MemLimit":        memLimit,
		"PVCSize":         pvcSize,
		"DashboardToken": func() string {
			const tokenBytes = 32
			b := make([]byte, tokenBytes)
			if _, err := cryptoRand.Read(b); err != nil {
				logger.Error("failed to generate dashboard token", "error", err)
				return ""
			}
			return hex.EncodeToString(b)
		}(),
	}

	tmpl, err := template.New("manifests").Parse(k8sManifestTemplate)
	if err != nil {
		return fmt.Errorf("template parse: %w", err)
	}

	manifestPath := filepath.Join(dir, "all.yaml")
	f, err := os.Create(manifestPath)
	if err != nil {
		return fmt.Errorf("create manifest: %w", err)
	}
	if err := tmpl.Execute(f, data); err != nil {
		f.Close()
		return fmt.Errorf("template exec: %w", err)
	}
	f.Close()

	cmd := exec.Command("kubectl", "apply", "-f", manifestPath)
	out, err := cmd.CombinedOutput()

	// Remove manifest immediately — it contains GitHub tokens in plaintext
	os.Remove(manifestPath)

	if err != nil {
		logger.Warn("kubectl apply failed", "hive", h.ID, "output", string(out), "error", err)
		return fmt.Errorf("provisioning failed — check hub logs for details")
	}

	logger.Info("audit: saas hive provisioned", "hive_id", h.ID, "owner", h.Owner, "org", h.Org)
	return nil
}

func StartProvisionWatcher(logger *slog.Logger, mu *sync.RWMutex) {
	ticker := time.NewTicker(provisionPollInterval)
	defer ticker.Stop()

	for range ticker.C {
		hives := listSaaSHives()
		for _, h := range hives {
			if h.Status != "provisioning" {
				continue
			}
			created, _ := time.Parse(time.RFC3339, h.CreatedAt)
			if time.Since(created) > provisionTimeout {
				h.Status = "error"
				h.Error = "provisioning timed out"
				saveSaaSHive(&h)
				logger.Warn("saas hive provision timeout", "hive_id", h.ID)
				continue
			}

			ns := "hive-hosted-" + h.ID
			cmd := exec.Command("kubectl", "get", "deployment", "hive", "-n", ns, "-o", "jsonpath={.status.availableReplicas}")
			out, err := cmd.Output()
			if err != nil {
				continue
			}
			if strings.TrimSpace(string(out)) == "1" {
				h.Status = "running"
				saveSaaSHive(&h)
				logger.Info("audit: saas hive running", "hive_id", h.ID)
			}
		}
	}
}

const k8sManifestTemplate = `apiVersion: v1
kind: Namespace
metadata:
  name: {{.Namespace}}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: hive-config
  namespace: {{.Namespace}}
data:
  hive.yaml: |
    project:
      org: {{.Org}}
      repos: {{.Repos}}
      primary_repo: {{.PrimaryRepo}}
    agents:
      guide:
        backend: copilot
        model: claude-sonnet-4-6
        enabled: true
      scanner:
        backend: copilot
        model: claude-sonnet-4-6
        enabled: true
    governor:
      eval_interval_s: 300
      modes:
        idle:
          threshold: 0
          guide: 4h
          scanner: 4h
        busy:
          threshold: 10
          guide: 2h
          scanner: 2h
    github:
{{- if .UseApp}}
      app_id: {{.AppID}}
      installation_id: {{.InstallationID}}
      key_file: /secrets/gh-app-key.pem
{{- else}}
      token: "${HIVE_GITHUB_TOKEN}"
{{- end}}
    dashboard:
      port: 3002
    hub:
      enabled: true
      url: https://hive.kubestellar.io
      is_public: true
    acmm_level: {{.ACMMLevel}}
---
apiVersion: v1
kind: Secret
metadata:
  name: hive-secrets
  namespace: {{.Namespace}}
type: Opaque
stringData:
  dashboard-token: {{.DashboardToken}}
{{- if .UseApp}}
  gh-app-key.pem: |
{{.AppPrivateKey}}
{{- else}}
  github-token: {{.Token}}
{{- end}}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: hive-data
  namespace: {{.Namespace}}
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: {{.PVCSize}}
  storageClassName: oci-bv
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hive
  namespace: {{.Namespace}}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: hive
      hive-id: {{.ID}}
  template:
    metadata:
      labels:
        app: hive
        hive-id: {{.ID}}
    spec:
      initContainers:
      - name: config-seed
        image: busybox
        command: ["sh", "-c", "test -f /data/hive.yaml || cp /etc/hive/hive.yaml /data/hive.yaml"]
        volumeMounts:
        - name: config
          mountPath: /etc/hive
          readOnly: true
        - name: data
          mountPath: /data
      containers:
      - name: hive
        image: ghcr.io/kubestellar/hive:v2-latest
        imagePullPolicy: Always
        env:
        - name: HIVE_CONFIG
          value: /data/hive.yaml
{{- if not .UseApp}}
        - name: HIVE_GITHUB_TOKEN
          valueFrom:
            secretKeyRef:
              name: hive-secrets
              key: github-token
{{- end}}
        - name: DASHBOARD_AUTH_TOKEN
          valueFrom:
            secretKeyRef:
              name: hive-secrets
              key: dashboard-token
        - name: HIVE_ID
          value: "{{.ID}}"
        - name: HIVE_LEVEL
          value: "{{.ACMMLevel}}"
        - name: HIVE_HUB_URL
          value: https://hive.kubestellar.io
        ports:
        - containerPort: 3002
        resources:
          requests:
            cpu: {{.CPURequest}}
            memory: {{.MemRequest}}
          limits:
            cpu: {{.CPULimit}}
            memory: {{.MemLimit}}
        volumeMounts:
        - name: config
          mountPath: /etc/hive
        - name: data
          mountPath: /data
{{- if .UseApp}}
        - name: secrets
          mountPath: /secrets
          readOnly: true
{{- end}}
      volumes:
      - name: config
        configMap:
          name: hive-config
      - name: data
        persistentVolumeClaim:
          claimName: hive-data
{{- if .UseApp}}
      - name: secrets
        secret:
          secretName: hive-secrets
{{- end}}
---
apiVersion: v1
kind: Service
metadata:
  name: hive
  namespace: {{.Namespace}}
spec:
  selector:
    app: hive
    hive-id: {{.ID}}
  ports:
  - name: http
    port: 3002
    targetPort: 3002
  type: ClusterIP
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: hive
  namespace: {{.Namespace}}
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/auth-url: "https://hive.kubestellar.io/api/saas/auth-check?hive={{.ID}}"
    nginx.ingress.kubernetes.io/auth-signin: "https://hive.kubestellar.io/login?redirect=$scheme://$http_host$request_uri"
    nginx.ingress.kubernetes.io/auth-response-headers: "X-Hive-User,X-Hive-Role"
spec:
  ingressClassName: nginx
  rules:
  - host: {{.ID}}.hive.kubestellar.io
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: hive
            port:
              number: 3002
  tls:
  - hosts:
    - {{.ID}}.hive.kubestellar.io
    secretName: hive-tls
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: hive-contribute
  namespace: {{.Namespace}}
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
spec:
  ingressClassName: nginx
  rules:
  - host: {{.ID}}.hive.kubestellar.io
    http:
      paths:
      - path: /api/contribute
        pathType: Prefix
        backend:
          service:
            name: hive
            port:
              number: 3002
  tls:
  - hosts:
    - {{.ID}}.hive.kubestellar.io
    secretName: hive-tls
`
