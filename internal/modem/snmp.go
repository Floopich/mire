package modem

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/snmp"
)

// Pilote SNMP standard, experimental : il lit les tables DOCSIS normalisees
// (DOCS-IF-MIB, RFC 4546, et DOCS-IF3-MIB de CableLabs) que beaucoup de
// modems publient en SNMP sur leur adresse de diagnostic. Il couvre les
// canaux SC-QAM : les canaux OFDM et OFDMA de DOCSIS 3.1 sont dans d'autres
// tables, propres a chaque constructeur dans la pratique.

// SNMPID est l'identifiant du pilote SNMP.
const SNMPID = "snmp"

// OID lues. Les colonnes des tables sont indexees par l'ifIndex du canal.
var (
	oidSysDescr   = snmp.MustOID("1.3.6.1.2.1.1.1.0")
	oidDocsisCap  = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.5.0")      // docsIfDocsisBaseCapability
	oidDSID       = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.1.1.1")    // docsIfDownChannelId
	oidDSFreq     = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.1.1.2")    // docsIfDownChannelFrequency, Hz
	oidDSMod      = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.1.1.4")    // docsIfDownChannelModulation
	oidDSPower    = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.1.1.6")    // docsIfDownChannelPower, dixiemes de dBmV
	oidUSID       = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.2.1.1")    // docsIfUpChannelId
	oidUSFreq     = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.2.1.2")    // docsIfUpChannelFrequency, Hz
	oidSigCorr    = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.4.1.3")    // docsIfSigQCorrecteds
	oidSigUncorr  = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.4.1.4")    // docsIfSigQUncorrectables
	oidSigSNR     = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.4.1.5")    // docsIfSigQSignalNoise, dixiemes de dB
	oidSigCorr64  = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.4.1.8")    // docsIfSigQExtCorrecteds
	oidSigUncor64 = snmp.MustOID("1.3.6.1.2.1.10.127.1.1.4.1.9")    // docsIfSigQExtUncorrectables
	oidUSTx3      = snmp.MustOID("1.3.6.1.4.1.4491.2.1.20.1.2.1.1") // docsIf3CmStatusUsTxPower, dixiemes de dBmV
	oidUSTxLegacy = snmp.MustOID("1.3.6.1.2.1.10.127.1.2.2.1.3")    // docsIfCmStatusTxPower
)

// SNMP lit un modem par SNMPv2c.
type SNMP struct {
	client *snmp.Client
}

// NewSNMP prepare le pilote. La communaute est le mot de passe s'il est
// renseigne, sinon l'identifiant, public par defaut.
func NewSNMP(c Config) (*SNMP, error) {
	host, err := snmpHost(c.URL)
	if err != nil {
		return nil, err
	}
	community := c.Password
	if community == "" {
		community = c.User
	}
	if community == "" {
		community = "public"
	}
	return &SNMP{client: &snmp.Client{Addr: host, Community: community, Timeout: 3 * time.Second, Retries: 1}}, nil
}

// snmpHost tire l'hote de l'adresse du modem : http://192.168.100.1 donne
// 192.168.100.1, port 161.
func snmpHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("adresse du modem vide")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("adresse du modem invalide : %q", raw)
	}
	return u.Hostname(), nil
}

// column lit une colonne de table, indexee par le dernier element de l'OID.
func (s *SNMP) column(ctx context.Context, root snmp.OID) (map[uint32]snmp.Value, error) {
	vbs, err := s.client.Walk(ctx, root)
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]snmp.Value, len(vbs))
	for _, vb := range vbs {
		if len(vb.OID) == len(root)+1 {
			out[vb.OID[len(vb.OID)-1]] = vb.Value
		}
	}
	return out, nil
}

func snmpNumber(v snmp.Value) (int64, bool) {
	switch v.Kind {
	case snmp.Integer:
		return v.Int, true
	case snmp.Unsigned:
		return int64(v.Uint), true
	}
	return 0, false
}

// tenths rend une valeur en dixiemes : 385 donne « 38.5 ».
func snmpTenths(v snmp.Value) string {
	n, ok := snmpNumber(v)
	if !ok {
		return ""
	}
	return strconv.FormatFloat(float64(n)/10, 'f', 1, 64)
}

// mhz rend une frequence en hertz en megahertz.
func snmpMHz(v snmp.Value) string {
	n, ok := snmpNumber(v)
	if !ok || n <= 0 {
		return ""
	}
	return strconv.FormatFloat(float64(n)/1e6, 'f', 3, 64) + " MHz"
}

// dsModulation traduit docsIfDownChannelModulation.
func dsModulation(v snmp.Value) string {
	switch n, _ := snmpNumber(v); n {
	case 3:
		return "64QAM"
	case 4:
		return "256QAM"
	}
	return ""
}

// Snapshot lit les canaux.
func (s *SNMP) Snapshot(ctx context.Context) (docsis.RawSnapshot, error) {
	var raw docsis.RawSnapshot
	cols := map[string]map[uint32]snmp.Value{}
	for name, oid := range map[string]snmp.OID{
		"dsid": oidDSID, "dsfreq": oidDSFreq, "dsmod": oidDSMod, "dspow": oidDSPower,
		"usid": oidUSID, "usfreq": oidUSFreq, "corr": oidSigCorr, "uncorr": oidSigUncorr, "snr": oidSigSNR,
		"corr64": oidSigCorr64, "uncorr64": oidSigUncor64, "ustx": oidUSTx3, "ustx0": oidUSTxLegacy,
	} {
		c, err := s.column(ctx, oid)
		if err != nil {
			if errors.Is(err, snmp.ErrTimeout) {
				return raw, fmt.Errorf("le modem ne répond pas en SNMP (communauté refusée, ou SNMP fermé) : %w", err)
			}
			return raw, err
		}
		cols[name] = c
	}
	if len(cols["dsid"]) == 0 {
		return raw, errors.New("le modem répond en SNMP, mais ne publie pas de canaux DOCSIS")
	}
	if caps, err := s.client.Get(ctx, oidDocsisCap); err == nil && len(caps) == 1 {
		switch n, _ := snmpNumber(caps[0].Value); n {
		case 4:
			raw.DOCSIS = "3.0"
		case 5:
			raw.DOCSIS = "3.1"
		}
	}
	counter := func(wide, narrow string, idx uint32) docsis.FlexInt {
		if v, ok := cols[wide][idx]; ok {
			if n, ok := snmpNumber(v); ok {
				return docsis.FlexInt(n)
			}
		}
		n, _ := snmpNumber(cols[narrow][idx])
		return docsis.FlexInt(n)
	}
	for _, idx := range keys(cols["dsid"]) {
		id, _ := snmpNumber(cols["dsid"][idx])
		raw.Downstream = append(raw.Downstream, docsis.RawDownstream{
			ChannelID:     docsis.FlexInt(id),
			Frequency:     snmpMHz(cols["dsfreq"][idx]),
			Modulation:    dsModulation(cols["dsmod"][idx]),
			PowerLevel:    snmpTenths(cols["dspow"][idx]),
			MSE:           snmpTenths(cols["snr"][idx]),
			CorrErrors:    counter("corr64", "corr", idx),
			NonCorrErrors: counter("uncorr64", "uncorr", idx),
		})
	}
	ups := keys(cols["usid"])
	for _, idx := range ups {
		id, _ := snmpNumber(cols["usid"][idx])
		power := snmpTenths(cols["ustx"][idx])
		if power == "" && len(ups) == 1 && len(cols["ustx0"]) == 1 {
			// Ancienne table : une seule puissance d'emission, sans canal.
			for _, v := range cols["ustx0"] {
				power = snmpTenths(v)
			}
		}
		raw.Upstream = append(raw.Upstream, docsis.RawUpstream{
			ChannelID: docsis.FlexInt(id), Frequency: snmpMHz(cols["usfreq"][idx]), PowerLevel: power,
		})
	}
	sort.Slice(raw.Downstream, func(i, j int) bool { return raw.Downstream[i].ChannelID < raw.Downstream[j].ChannelID })
	sort.Slice(raw.Upstream, func(i, j int) bool { return raw.Upstream[i].ChannelID < raw.Upstream[j].ChannelID })
	return raw, nil
}

func keys(m map[uint32]snmp.Value) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SNMPProbe dit si une adresse repond en SNMP et ce qu'elle publie : la
// description du systeme et le nombre de canaux descendants. Elle sert au
// rapport « Proposer mon modem ».
type SNMPProbe struct {
	Answered  bool
	SysDescr  string
	DOCSIS    string
	DSChannel int
	USChannel int
	Err       string
}

// ProbeSNMP essaie la communaute donnee, public sinon.
func ProbeSNMP(ctx context.Context, address, community string) SNMPProbe {
	var p SNMPProbe
	d, err := NewSNMP(Config{URL: address, User: community})
	if err != nil {
		p.Err = err.Error()
		return p
	}
	d.client.Retries = 0
	vbs, err := d.client.Get(ctx, oidSysDescr, oidDocsisCap)
	if err != nil {
		p.Err = err.Error()
		return p
	}
	p.Answered = true
	if len(vbs) > 0 && vbs[0].Value.Kind == snmp.Octets {
		p.SysDescr = string(vbs[0].Value.Bytes)
	}
	if len(vbs) > 1 {
		switch n, _ := snmpNumber(vbs[1].Value); n {
		case 4:
			p.DOCSIS = "3.0"
		case 5:
			p.DOCSIS = "3.1"
		}
	}
	if c, err := d.column(ctx, oidDSID); err == nil {
		p.DSChannel = len(c)
	}
	if c, err := d.column(ctx, oidUSID); err == nil {
		p.USChannel = len(c)
	}
	return p
}
