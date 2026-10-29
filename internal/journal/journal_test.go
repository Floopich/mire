package journal

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func TestLeJournalGardeLesDernieresLignes(t *testing.T) {
	var out bytes.Buffer
	j, log := New(&out)
	log.Info("démarré", "version", "v1")
	log.Debug("caché")
	log.With("depuis", "Mire").Warn("refusé", "erreur", "le modem dit non")
	e := j.Entries()
	if len(e) != 2 || e[0].Message != "refusé" || e[0].Attrs != `depuis=Mire erreur="le modem dit non"` || e[1].Attrs != "version=v1" {
		t.Fatalf("lignes : %+v", e)
	}
	if !strings.Contains(out.String(), "msg=démarré") || strings.Contains(out.String(), "caché") {
		t.Errorf("sortie : %s", out.String())
	}
}

func TestLeNiveauChangeAussitot(t *testing.T) {
	var out bytes.Buffer
	j, log := New(&out)
	j.SetLevel(Detailed)
	log.Debug("détail")
	j.SetLevel(Errors)
	log.Info("info")
	log.Error("erreur")
	e := j.Entries()
	if len(e) != 2 || e[0].Message != "erreur" || e[1].Message != "détail" {
		t.Fatalf("lignes : %+v", e)
	}
}

func TestLeJournalTourne(t *testing.T) {
	j, log := New(&bytes.Buffer{})
	for i := 0; i < Capacity+10; i++ {
		log.Info(strconv.Itoa(i))
	}
	e := j.Entries()
	if len(e) != Capacity || e[0].Message != strconv.Itoa(Capacity+9) || e[len(e)-1].Message != "10" {
		t.Fatalf("%d lignes, de %s à %s", len(e), e[0].Message, e[len(e)-1].Message)
	}
}
