package probe

import (
	"testing"
	"time"
)

func TestGigue(t *testing.T) {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	p := &Prober{}
	// 10, 14, 11 : ecarts de 4 et 3 ms. L'echec coupe la suite : 30 ne se
	// compare pas a 11. Puis 30, 32 : un ecart de 2 ms.
	for _, r := range []struct {
		ok  bool
		rtt time.Duration
	}{{true, ms(10)}, {true, ms(14)}, {true, ms(11)}, {false, 0}, {true, ms(30)}, {true, ms(32)}} {
		p.agg.add(time.Time{}, r.ok, r.rtt)
		p.jitter(r.ok, r.rtt)
	}
	s := p.agg.sample()
	if s.JitterPairs != 3 || s.Jitter == nil || *s.Jitter != 3 {
		t.Fatalf("gigue %v sur %d paires, attendu 3 ms sur 3", s.Jitter, s.JitterPairs)
	}
	// Sans deux reussites de suite, pas de gigue : ni zero, ni valeur inventee.
	q := &Prober{}
	q.agg.add(time.Time{}, true, ms(10))
	q.jitter(true, ms(10))
	if s := q.agg.sample(); s.Jitter != nil || s.JitterPairs != 0 {
		t.Errorf("une seule tentative : %v", s.Jitter)
	}
}
