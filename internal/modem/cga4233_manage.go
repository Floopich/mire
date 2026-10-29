package modem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Gestion du CGA4233 : informations, reseau domestique, redemarrage.
//
// Les chemins viennent de l'interface web du modem. Hors des tables DOCSIS,
// le firmware enveloppe ses reponses : {"error":"ok","message":…,"data":{…}}.

const (
	systemFields = "ModelName,Manufacturer,HardwareVersion,SoftwareVersion,FirmwareName,UpTime,LanMode,CMStatus"
	systemPath   = "/api/v1/system/" + systemFields
	dhcpPath     = "/api/v1/dhcp/v4/1/IPAddressRT,SubnetMaskRT,PoolEnable,DNSTblRT"
	hostPath     = "/api/v1/host/hostTbl"
	wifiPath     = "/api/v1/wifi/1,2/RadioEnable,SSIDEnable,SSID,SSIDAdvertisementEnabled,Channel," +
		"AutoChannelEnable,OperatingChannelBandwidth,OperatingStandards,ModeEnabled"

	// restartTargets redemarre tout le boitier, comme le bouton de l'interface.
	restartTargets = "Router,Wifi,VoIP,Dect,MoCA"
)

// capturePaths sont les lectures du diagnostic : uniquement des GET, qui ne
// modifient rien. Ils couvrent aussi des chemins que Mire n'utilise pas
// encore, pour savoir ce que chaque firmware expose.
var capturePaths = []struct{ label, path string }{
	{"informations du modem", "/api/v1/system/HardwareVersion,FirmwareName,UpTime,LocalTime,LanMode,ModelName,CMStatus,Manufacturer," +
		"SoftwareVersion,BootloaderVersion,CoreVersion,FirmwareBuildTime,ProcessorSpeed,Hardware,MemTotal,MemFree," +
		"SerialNumber,CMMACAddress,MACAddressRT"},
	{"canaux DOCSIS", docsisPath},
	{"réseau local", "/api/v1/dhcp/v4/1/IPAddressRT,SubnetMaskRT,IPAddressGW,DNSTblRT,PoolEnable,WanAddressMode"},
	{"appareils connectés", "/api/v1/host/hostTbl,LanMode,MixedMode,LanPortMode"},
	{"Wi-Fi", "/api/v1/wifi/1,2/RadioEnable,OperatingStandards,Channel,OperatingChannelBandwidth,AutoChannelEnable," +
		"SSIDEnable,SSID,BSSID,ModeEnabled,EncryptionMethod,SSIDAdvertisementEnabled,RegulatoryDomain"},
	{"interfaces", "/api/v1/dig_interface"},
	{"état DOCSIS", "/api/v1/sta_docsis_status"},
	{"session", "/api/v1/session/init_page"},
	{"configuration de connexion", "/api/v1/login_conf"},
	{"menu de l'interface", "/api/v1/session/menu"},
}

type envelope struct {
	Error   string          `json:"error"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e envelope) ok() bool { return e.Error == "ok" || (e.Error == "" && len(e.Data) > 0) }

// unauthorized reconnait la reponse d'une session expiree : ce firmware la
// sert parfois avec un statut 200.
func unauthorized(body []byte) bool {
	var e envelope
	return json.Unmarshal(body, &e) == nil && e.Error == "error" &&
		strings.Contains(strings.ToLower(e.Message), "unauthorized")
}

// fetchBody lit un chemin et rejoue l'authentification une fois si la
// reponse dit que la session a expire.
func (m *CGA4233) fetchBody(ctx context.Context, path string) ([]byte, error) {
	body, err := m.fetch(ctx, path)
	if err != nil || !unauthorized(body) {
		return body, err
	}
	m.invalidate()
	if err := m.login(ctx); err != nil {
		return nil, err
	}
	body, err = m.fetch(ctx, path)
	if err == nil && unauthorized(body) {
		m.invalidate()
		return nil, fmt.Errorf("%w : session refusée sur %s", ErrAuth, path)
	}
	return body, err
}

// fetchFields lit une reponse enveloppee et rend ses champs.
func (m *CGA4233) fetchFields(ctx context.Context, path string) (map[string]json.RawMessage, error) {
	body, err := m.fetchBody(ctx, path)
	if err != nil {
		return nil, err
	}
	return fieldsOf(path, body)
}

func fieldsOf(path string, body []byte) (map[string]json.RawMessage, error) {
	var e envelope
	if err := json.Unmarshal(body, &e); err != nil {
		return nil, fmt.Errorf("%s : réponse illisible : %s", path, truncate(body))
	}
	if !e.ok() {
		return nil, fmt.Errorf("%s : %s", path, firstNonEmpty(e.Message, e.Error))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(e.Data, &fields); err != nil {
		return nil, fmt.Errorf("%s : données illisibles : %s", path, truncate(e.Data))
	}
	return fields, nil
}

// Info lit le modele, les versions, la duree de fonctionnement et le mode.
func (m *CGA4233) Info(ctx context.Context) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := m.fetchFields(ctx, systemPath)
	if err != nil {
		return Info{}, err
	}
	return parseInfo(f), nil
}

func parseInfo(f map[string]json.RawMessage) Info {
	info := Info{
		Model:        text(f["ModelName"]),
		Manufacturer: text(f["Manufacturer"]),
		Hardware:     text(f["HardwareVersion"]),
		Software:     firstNonEmpty(text(f["SoftwareVersion"]), text(f["FirmwareName"])),
		Status:       text(f["CMStatus"]),
	}
	info.UptimeText = text(f["UpTime"])
	info.Uptime = parseUptime(info.UptimeText)
	// Le firmware VOO ecrit « bridge-static » ; d'autres « router » ou « modem ».
	switch mode := strings.ToLower(text(f["LanMode"])); {
	case strings.Contains(mode, "bridge"), mode == "modem":
		info.Mode = ModeBridge
	case strings.Contains(mode, "router"):
		info.Mode = ModeRouter
	}
	return info
}

var uptimeUnit = regexp.MustCompile(`(?i)(\d+)\s*(days?|jours?|d|j|hours?|heures?|h|minutes?|min|m|seconds?|secondes?|sec|s)\b`)

// parseUptime comprend des secondes seules, « 3 days 04:05:06 », « 04:05:06 »
// et « 3d 4h 5m 6s ». Un format inconnu rend zero.
func parseUptime(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	var d time.Duration
	rest := s
	if i := strings.LastIndexByte(s, ' '); strings.Count(s, ":") == 2 {
		clock := s
		if i >= 0 {
			clock, rest = s[i+1:], s[:i]
		} else {
			rest = ""
		}
		parts := strings.Split(clock, ":")
		var hms [3]int64
		for k, p := range parts {
			n, err := strconv.ParseInt(p, 10, 64)
			if err != nil || n < 0 {
				return 0
			}
			hms[k] = n
		}
		d = time.Duration(hms[0])*time.Hour + time.Duration(hms[1])*time.Minute + time.Duration(hms[2])*time.Second
	}
	matches := uptimeUnit.FindAllStringSubmatch(rest, -1)
	if len(matches) == 0 && strings.TrimSpace(rest) != "" {
		return 0
	}
	for _, mm := range matches {
		n, _ := strconv.ParseInt(mm[1], 10, 64)
		switch u := strings.ToLower(mm[2]); {
		case strings.HasPrefix(u, "d"), strings.HasPrefix(u, "j"):
			d += time.Duration(n) * 24 * time.Hour
		case strings.HasPrefix(u, "h"):
			d += time.Duration(n) * time.Hour
		case strings.HasPrefix(u, "m"):
			d += time.Duration(n) * time.Minute
		default:
			d += time.Duration(n) * time.Second
		}
	}
	return d
}

// Router lit le reseau local, le Wi-Fi et les appareils. Une partie
// illisible n'empeche pas les autres : elle est nommee dans Missing.
func (m *CGA4233) Router(ctx context.Context) (Router, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r Router
	var errs []error

	if f, err := m.fetchFields(ctx, dhcpPath); err != nil {
		errs = append(errs, err)
		r.Missing = append(r.Missing, "réseau local")
	} else {
		r.LANAddress = firstOf(addresses(f["IPAddressRT"]))
		r.LANMask = text(f["SubnetMaskRT"])
		if raw, ok := f["PoolEnable"]; ok && text(raw) != "" {
			on := truthy(text(raw))
			r.DHCP = &on
		}
		r.DNS = addresses(f["DNSTblRT"])
	}

	if body, err := m.fetchBody(ctx, wifiPath); err != nil {
		errs = append(errs, err)
		r.Missing = append(r.Missing, "Wi-Fi")
	} else if radios, err := parseRadios(body); err != nil {
		errs = append(errs, err)
		r.Missing = append(r.Missing, "Wi-Fi")
	} else {
		r.Radios = radios
	}

	if f, err := m.fetchFields(ctx, hostPath); err != nil {
		errs = append(errs, err)
		r.Missing = append(r.Missing, "appareils")
	} else {
		r.Hosts = parseHosts(f["hostTbl"])
	}

	if len(errs) == 3 {
		return Router{}, errors.Join(errs...)
	}
	return r, nil
}

// parseRadios lit la reponse Wi-Fi : une enveloppe par radio, {"1":…,"2":…}.
func parseRadios(body []byte) ([]Radio, error) {
	var byRadio map[string]json.RawMessage
	if err := json.Unmarshal(body, &byRadio); err != nil {
		return nil, fmt.Errorf("Wi-Fi : réponse illisible : %s", truncate(body))
	}
	ids := make([]string, 0, len(byRadio))
	for id := range byRadio {
		if _, err := strconv.Atoi(id); err == nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []Radio
	for _, id := range ids {
		f, err := fieldsOf("Wi-Fi "+id, byRadio[id])
		if err != nil {
			continue
		}
		standard := text(f["OperatingStandards"])
		r := Radio{
			Band:        band(id, standard),
			Enabled:     truthy(text(f["RadioEnable"])) && (text(f["SSIDEnable"]) == "" || truthy(text(f["SSIDEnable"]))),
			SSID:        text(f["SSID"]),
			Channel:     text(f["Channel"]),
			AutoChannel: truthy(text(f["AutoChannelEnable"])),
			Width:       text(f["OperatingChannelBandwidth"]),
			Standard:    standard,
			Security:    text(f["ModeEnabled"]),
		}
		if adv := text(f["SSIDAdvertisementEnabled"]); adv != "" {
			r.Hidden = !truthy(adv)
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, errors.New("Wi-Fi : aucune radio lisible")
	}
	return out, nil
}

// band deduit la bande des normes annoncees ; a defaut, la convention du
// firmware : radio 1 en 2,4 GHz, radio 2 en 5 GHz.
func band(id, standards string) string {
	s := strings.ToLower(standards)
	has := func(t string) bool {
		for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '/' || r == ' ' }) {
			if p == t {
				return true
			}
		}
		return false
	}
	switch {
	case has("b") || has("g"):
		return "2,4 GHz"
	case has("a") || has("ac"):
		return "5 GHz"
	case id == "1":
		return "2,4 GHz"
	case id == "2":
		return "5 GHz"
	}
	return "Radio " + id
}

func parseHosts(raw json.RawMessage) []Host {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		return nil
	}
	out := make([]Host, 0, len(rows))
	for _, row := range rows {
		h := Host{
			Name:   text(row["hostname"]),
			IP:     text(row["ipaddress"]),
			MAC:    strings.ToLower(text(row["physaddress"])),
			Active: truthy(text(row["active"])),
		}
		if h.Name == "" && h.IP == "" && h.MAC == "" {
			continue
		}
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Restart demande le redemarrage complet du modem.
//
// C'est l'appel du bouton « Redémarrer le système » de l'interface web du
// firmware VOO (views/adm_reset.html) : POST api/v1/reset avec
// reboot=Router,Wifi,VoIP,Dect,MoCA et ui_access=reboot_device. Le meme
// chemin sert a la reinitialisation d'usine, avec la cle « reset » : ce
// formulaire n'en contient jamais. Une demande restee sans reponse n'est
// jamais rejouee : le modem est peut-etre deja en train de redemarrer.
func (m *CGA4233) Restart(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token == "" {
		if err := m.login(ctx); err != nil {
			return err
		}
	}
	defer m.invalidate()

	body, status, err := m.post(ctx, restartPath, restartForm(), m.token)
	if status == http.StatusUnauthorized || status == http.StatusForbidden || (err == nil && unauthorized(body)) {
		m.invalidate()
		if err := m.login(ctx); err != nil {
			return err
		}
		body, status, err = m.post(ctx, restartPath, restartForm(), m.token)
	}
	// Comme l'interface web, une reponse HTTP 200 vaut acceptation, quel
	// qu'en soit le contenu : certains firmwares repondent « failure
	// parameters! » et redemarrent quand meme.
	switch {
	case err != nil && status == 0:
		return noAnswer(ctx, err)
	case err != nil && refused(body):
		return fmt.Errorf("%w : %s", ErrRestartRefused, describe(body, status))
	case err != nil:
		// Le firmware VOO redemarre parfois avant d'avoir fini de repondre.
		return fmt.Errorf("%w : %s", ErrNoAnswer, describe(body, status))
	default:
		return nil
	}
}

const restartPath = "/api/v1/reset"

// restartForm est le formulaire du redemarrage complet, et lui seul.
func restartForm() url.Values {
	return url.Values{"reboot": {restartTargets}, "ui_access": {"reboot_device"}}
}

// refused reconnait une reponse qui dit non : {"error":"error",…}.
func refused(body []byte) bool {
	var e envelope
	return json.Unmarshal(body, &e) == nil && e.Error != "" && e.Error != "ok"
}

func noAnswer(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w : %v", ErrNoAnswer, err)
}

// post envoie un formulaire. Le statut est nul quand la requete n'a pas
// abouti : aucune reponse n'est arrivee.
func (m *CGA4233) post(ctx context.Context, path string, form url.Values, token string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	m.applyHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	if token != "" {
		req.Header.Set("X-CSRF-TOKEN", token)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s : %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, fmt.Errorf("%s : HTTP %d", path, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func describe(body []byte, status int) string {
	var e envelope
	if json.Unmarshal(body, &e) == nil && (e.Message != "" || e.Error != "") {
		return firstNonEmpty(e.Message, e.Error)
	}
	if status != 0 && status != http.StatusOK {
		return "HTTP " + strconv.Itoa(status)
	}
	return "réponse inattendue"
}

// Capture lit chaque chemin connu et rend les reponses anonymisees.
func (m *CGA4233) Capture(ctx context.Context, progress Progress) ([]Capture, error) {
	total := len(capturePaths) + 1
	progress.report(0, total, "attente du modem")
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token == "" {
		progress.report(0, total, "connexion au modem")
		if err := m.login(ctx); err != nil {
			return nil, err
		}
	}
	out := make([]Capture, 0, total)
	for i, p := range capturePaths {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		progress.report(i, total, p.label)
		c := Capture{Path: p.path}
		body, status, err := m.authenticatedGet(ctx, p.path)
		c.Status = status
		if err != nil {
			c.Error = scrub(err.Error())
		}
		if len(body) > 0 {
			if json.Valid(body) {
				c.Body = Anonymize(body)
			} else {
				c.Text = clip(scrub(string(body)), 2000)
			}
		}
		out = append(out, c)
	}
	progress.report(total-1, total, "scripts de l'interface web")
	return append(out, m.uiCalls(ctx)...), nil
}

// uiCalls lit l'interface web du modem, session ouverte : les scripts
// charges apres connexion disent aussi comment le firmware redemarre.
func (m *CGA4233) uiCalls(ctx context.Context) []Capture {
	home, status, err := m.authenticatedGet(ctx, "/")
	if err != nil {
		return []Capture{{Path: uiCallsPath, Status: status, Error: scrub(err.Error())}}
	}
	seeds := map[string][]byte{"/": home}
	// Le menu de session liste les ecrans de l'interface et leurs fichiers.
	if menu, _, err := m.authenticatedGet(ctx, "/api/v1/session/menu"); err == nil {
		seeds["/api/v1/session/menu"] = menu
	}
	return scriptCalls(ctx, seeds, func(ctx context.Context, path string) ([]byte, error) {
		b, _, err := m.authenticatedGet(ctx, path)
		return b, err
	})
}

// text rend une valeur JSON sous forme de texte : chaine, nombre, booleen,
// ou premier element d'une liste.
func text(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return strconv.FormatBool(b)
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
		return text(list[0])
	}
	return ""
}

// addresses rassemble les adresses IP d'une valeur, quelle que soit sa
// forme : chaine, liste, ou table d'objets.
func addresses(raw json.RawMessage) []string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			for _, f := range strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
				if a, err := netip.ParseAddr(f); err == nil && !a.IsUnspecified() && !seen[a.String()] {
					seen[a.String()] = true
					out = append(out, a.String())
				}
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k])
			}
		}
	}
	walk(v)
	return out
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "on", "yes", "enabled", "enable", "up":
		return true
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstOf(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
