// Commande mire : collecte les releves DOCSIS et sert l'interface.
//
// La collecte tourne en tache de fond ; l'interface reste disponible quand
// le modem ne repond pas.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	// Base des fuseaux embarquee : l'image minimale n'en contient pas, et
	// sans elle TZ=Europe/Brussels serait ignore et les heures affichees en UTC.
	_ "time/tzdata"

	"github.com/floopich/mire/internal/auth"
	"github.com/floopich/mire/internal/cli"
	"github.com/floopich/mire/internal/collect"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/service"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
	"github.com/floopich/mire/internal/webview"
)

// version est fixee a la compilation : -ldflags "-X main.version=v1.2.3".
var version = ""

const usage = `Mire : analyse en continu d'une ligne DOCSIS.

Utilisation :
  mire [options]                 collecte les relevés et sert l'interface
  mire releve [options]          prend un seul relevé et l'enregistre
  mire mot-de-passe [options]    change le mot de passe de l'interface
  mire sauvegarde [options] FICHIER
                                 copie la base dans FICHIER, même en marche ;
                                 - écrit sur la sortie standard
  mire restauration [options] FICHIER
                                 remplace la base par une sauvegarde, Mire
                                 arrêté ; - lit l'entrée standard
  mire sante [options]           vérifie qu'une instance répond
  mire detecter                  cherche le modem sur le réseau
  mire diagnostic [options]      écrit les réponses du modem, anonymisées,
                                 à joindre à une demande ; lecture seule
  mire modeles                   liste les modems pris en charge
  mire version                   affiche la version

Au premier démarrage, ouvrez l'interface : elle demande un mot de passe,
cherche le modem et le teste. Les options (mire -h) et les variables
d'environnement MIRE_* imposent un réglage et le verrouillent dans l'interface.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Erreur :", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "", "service":
		return serve(args)
	case "releve":
		return snapshot(args)
	case "mot-de-passe":
		return setPassword(args)
	case "sauvegarde":
		return backup(args)
	case "restauration":
		return restore(args)
	case "sante":
		return health(args)
	case "detecter":
		return detect(args)
	case "diagnostic":
		return diagnostic(args)
	case "modeles":
		listModels(os.Stdout)
		return nil
	case "version":
		fmt.Println(currentVersion())
		return nil
	case "aide", "help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("commande inconnue : %q", cmd)
	}
}

func currentVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}

func newLogger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// open ouvre la base : prealable commun a toutes les commandes.
func open(cfg config) (*sql.DB, error) {
	if err := checkWritable(cfg.DataDir); err != nil {
		return nil, err
	}
	path := filepath.Join(cfg.DataDir, "mire.db")
	db, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	// La base contient le mot de passe du modem : elle n'est lisible que par
	// le compte du service. SQLite donne aux fichiers -wal et -shm les droits
	// de la base, mais ceux qui existent deja sont corriges aussi.
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(f, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

func serve(args []string) error {
	cfg, err := parseConfig("mire", args, os.Getenv, os.Stderr)
	if err != nil {
		return err
	}
	logger := newLogger(cfg.Verbose)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// MIRE_DEMO : seule la demonstration tourne, dans une base jetable ; les
	// vraies donnees ne sont pas ouvertes.
	if cfg.Demo {
		if os.Getenv("TZ") == "" {
			if loc, err := time.LoadLocation("Europe/Brussels"); err == nil {
				time.Local = loc
			}
		}
		d, err := startDemo(ctx, logger, false)
		if err != nil {
			return err
		}
		defer d.Close()
		return listen(ctx, stop, cfg, logger, d.handler, nil, false)
	}

	db, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	applyTimeZone(ctx, db, cfg, logger)

	// L'interface demarre toujours, meme sans modem configure.
	mgr := service.New(db, cfg.Forced, logger)
	mgr.Version = currentVersion()
	if err := mgr.Start(ctx); err != nil {
		return err
	}
	defer mgr.Stop()
	if st := mgr.State(); !st.Profile.Calibrated {
		logger.Warn("opérateur sans seuils : relevés conservés, ligne non qualifiée",
			"profil", st.Profile.ID)
	}

	srv, err := webview.NewServer(db, webview.Options{
		DataDir: cfg.DataDir,
		Version: currentVersion(),
		Service: mgr,
		Logger:  logger,
		// Avant l'installation, l'accueil propose d'essayer Mire sur une
		// ligne fictive, a cote de l'instance, sans toucher a sa base.
		StartDemo: func(ctx context.Context) (http.Handler, func(), error) {
			d, err := startDemo(ctx, logger.With("demo", true), true)
			if err != nil {
				return nil, nil, err
			}
			return d.handler, d.Close, nil
		},
	})
	if err != nil {
		return err
	}
	defer srv.Close()

	// Les sessions expirees sont purgees une fois par jour.
	var wg sync.WaitGroup
	sessions := auth.NewSessions(db)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := sessions.Prune(ctx); err != nil {
					logger.Warn("purge des sessions", "erreur", err)
				}
			}
		}
	}()
	firstVisit := false
	if ok, err := auth.HasPassword(ctx, db); err == nil && !ok {
		firstVisit = true
	}
	err = listen(ctx, stop, cfg, logger, srv.Routes(), &wg, firstVisit)
	return err
}

// listen sert l'interface jusqu'a l'arret demande.
func listen(ctx context.Context, stop func(), cfg config, logger *slog.Logger, handler http.Handler, wg *sync.WaitGroup, firstVisit bool) error {
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	port := cfg.Listen
	if _, p, err := net.SplitHostPort(cfg.Listen); err == nil {
		port = p
	}
	logger.Info("Mire " + currentVersion() + " : interface sur le port " + port)
	if firstVisit {
		logger.Info("première visite : ouvrez l'interface pour configurer Mire")
	}
	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		stop()
	} else {
		err = nil
	}
	if wg != nil {
		wg.Wait()
	}
	if err == nil {
		logger.Info("arrêté")
	}
	return err
}

// snapshot prend un releve, l'enregistre et l'affiche : c'est le test
// d'installation, avant de laisser tourner le service.
func snapshot(args []string) error {
	cfg, err := parseConfig("mire releve", args, os.Getenv, os.Stderr)
	if err != nil {
		return err
	}
	db, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st, err := resolveModem(ctx, db, cfg)
	if err != nil {
		return err
	}
	driver, err := st.Driver()
	if err != nil {
		return err
	}
	snap, err := collect.New(driver, db, collect.Options{}).CollectOnce(ctx)
	if err != nil {
		if !errors.Is(err, modem.ErrAuth) {
			fmt.Fprintln(os.Stderr, "Le modem ne répond pas à", st.Address, ": « mire detecter » le cherche sur le réseau.")
		}
		return err
	}
	fmt.Printf("Relevé enregistré : %s, %d canaux descendants, %d montants (DOCSIS %s).\n",
		st.Model.Label(), len(snap.Downstream), len(snap.Upstream), snap.DOCSIS)
	return nil
}

// resolveModem calcule la configuration et s'assure d'avoir le mot de passe
// du modem.
func resolveModem(ctx context.Context, db *sql.DB, cfg config) (service.State, error) {
	st, err := service.Resolve(ctx, db, cfg.Forced)
	if err != nil {
		return service.State{}, err
	}
	if st.ModelErr != nil {
		return service.State{}, st.ModelErr
	}
	// Un mot de passe absent de l'environnement et de la page Reglages se
	// saisit au terminal, sans jamais etre enregistre.
	if st.Values.String(settings.ModemPassword) == "" {
		if !cli.IsTerminal(os.Stdin) {
			return service.State{}, errors.New("renseignez le mot de passe du modem dans les réglages ou dans MIRE_MODEM_MOT_DE_PASSE")
		}
		saisi, err := cli.ReadPassword("Mot de passe du modem : ", true)
		if err != nil {
			return service.State{}, err
		}
		forced := settings.Source{}
		for k, v := range cfg.Forced {
			forced[k] = v
		}
		forced[settings.ModemPassword] = strings.TrimSpace(saisi)
		if st, err = service.Resolve(ctx, db, forced); err != nil {
			return service.State{}, err
		}
	}
	return st, nil
}

// diagnostic ecrit les reponses brutes du modem, anonymisees, pour une
// demande d'ajout ou de correction. Lecture seule : rien n'est modifie sur
// le modem ni enregistre dans la base.
func diagnostic(args []string) error {
	cfg, err := parseConfig("mire diagnostic", args, os.Getenv, os.Stderr)
	if err != nil {
		return err
	}
	db, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	st, err := service.Resolve(ctx, db, cfg.Forced)
	if err != nil {
		return err
	}
	// Un modele pris en charge ajoute ses lectures authentifiees ; sinon,
	// seules les pages publiques du modem sont lues.
	var driver modem.Driver
	if st.ModelErr == nil {
		if st, err = resolveModem(ctx, db, cfg); err != nil {
			return err
		}
		if driver, err = st.Driver(); err != nil {
			return err
		}
	}
	if st.Address == "" {
		return errors.New("indiquez l'adresse du modem : mire diagnostic -modem http://192.168.100.1")
	}
	fmt.Fprintln(os.Stderr, "Lecture du modem", st.Address, "(une minute ou deux)…")
	r := modem.BuildReport(ctx, st.Address, driver, func(done, total int, step string) {
		fmt.Fprintf(os.Stderr, "  %d/%d %s\n", done+1, total, step)
	})
	r.Mire = currentVersion()
	if st.ModelErr == nil {
		r.Model = st.Model.ID
	}
	if _, err := fmt.Print(modem.Readable(r)); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Terminé. Adresses MAC, numéro de série, noms de réseau et d'appareils, adresses IP publiques",
		"sont masqués : relisez quand même le fichier, puis joignez-le à une demande :\n  "+modem.IssueURL)
	return nil
}

// setPassword change le mot de passe de l'interface.
//
// C'est le recours quand le mot de passe est perdu : il exige un acces a la
// machine.
func setPassword(args []string) error {
	cfg, err := parseConfig("mire mot-de-passe", args, os.Getenv, os.Stderr)
	if err != nil {
		return err
	}
	password, err := readNewPassword(os.Stdin)
	if err != nil {
		return err
	}
	db, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := auth.SetPassword(context.Background(), db, password); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Mot de passe changé. Les sessions ouvertes ont été fermées.")
	return nil
}

// backup copie la base dans un fichier, sans arreter le service : c'est la
// sauvegarde a planifier, ou a faire avant une mise a jour.
func backup(args []string) error {
	cfg, rest, err := parseConfigArgs("mire sauvegarde", args, os.Getenv, os.Stderr)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("indiquez le fichier de destination : mire sauvegarde /chemin/mire-sauvegarde.db, ou - pour la sortie standard")
	}
	target := rest[0]
	if target == "-" {
		return backupToStdout(context.Background(), cfg)
	}
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("%s existe déjà : choisissez un autre nom", target)
	}
	db, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := store.Backup(context.Background(), db, target); err != nil {
		return err
	}
	// La copie contient le mot de passe du modem, comme la base.
	if err := os.Chmod(target, 0o600); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Sauvegarde écrite :", target)
	return nil
}

// readNewPassword lit le nouveau mot de passe : deux saisies masquees sur un
// terminal, une ligne sur l'entree standard sinon (docker exec -i, script).
func readNewPassword(in *os.File) (string, error) {
	if !cli.IsTerminal(in) {
		line, err := cli.ReadLine(in)
		return strings.TrimSpace(line), err
	}
	first, err := cli.ReadPassword("Nouveau mot de passe : ", true)
	if err != nil {
		return "", err
	}
	second, err := cli.ReadPassword("Confirmation : ", true)
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("les deux saisies diffèrent")
	}
	return strings.TrimSpace(first), nil
}

// health interroge /sante : c'est la sonde du HEALTHCHECK de l'image, qui ne
// contient ni curl ni wget.
func health(args []string) error {
	cfg, err := parseConfig("mire sante", args, os.Getenv, os.Stderr)
	if err != nil {
		return err
	}
	url, err := healthURL(cfg.Listen)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s : HTTP %d", url, resp.StatusCode)
	}
	return nil
}

// healthURL derive l'adresse locale de /sante depuis l'adresse d'ecoute.
func healthURL(listen string) (string, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("adresse d'ecoute %q: %w", listen, err)
	}
	return "http://127.0.0.1:" + port + "/sante", nil
}

// listModels affiche les modems pris en charge.
func listModels(w io.Writer) {
	fmt.Fprintln(w, "Modems pris en charge (MIRE_MODELE) :")
	for _, m := range modem.Models() {
		fmt.Fprintf(w, "  %-12s %s\n", m.ID, m.Label())
	}
	fmt.Fprintf(w, "  %-12s %s\n", modem.Generic, "autre modem, non pris en charge : la sonde Internet seule")
	fmt.Fprintln(w, "\nVotre modem n'y est pas ? Choisissez generique, puis « Proposer mon modem » sur la page Modem.")
}

// newIssueURL ouvre le formulaire « Nouveau modem » du depot.
const newIssueURL = modem.IssueURL

// detect cherche le modem sur les adresses usuelles et propose le reglage.
func detect(args []string) error {
	cfg, err := parseConfig("mire detecter", args, os.Getenv, os.Stderr)
	if err != nil {
		return err
	}
	db, err := open(cfg)
	if err != nil {
		return err
	}
	st, err := service.Resolve(context.Background(), db, cfg.Forced)
	_ = db.Close()
	if err != nil {
		return err
	}
	hosts := modem.Hosts(st.Values.String(settings.ModemURL), st.Profile.DefaultModemURL)
	fmt.Printf("Recherche du modem sur %s…\n\n", strings.Join(hosts, ", "))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	found := modem.Detect(ctx, hosts)
	if len(found) == 0 {
		return errors.New("aucun appareil ne répond : vérifiez que cette machine est reliée au modem")
	}
	for _, c := range found {
		title := c.Title
		if title == "" {
			title = "page sans titre"
		}
		state := "autre appareil réseau"
		switch {
		case c.Model != nil:
			state = "reconnu : " + c.Model.Label()
		case c.DOCSIS:
			state = "modem câble pas encore pris en charge"
		}
		fmt.Printf("  %-26s %s\n  %-26s %s\n", c.URL, title, "", state)
	}

	best := found[0]
	fmt.Println()
	switch {
	case best.Model != nil:
		fmt.Printf("Réglage conseillé, dans la page Réglages ou par variables :\n  MIRE_MODEM=%s\n  MIRE_MODELE=%s\n", best.URL, best.Model.ID)
	case best.DOCSIS:
		fmt.Printf("Un modem câble répond sur %s, mais Mire ne le connaît pas encore.\n"+
			"Demandez sa prise en charge en joignant son rapport (mire diagnostic -modem %s) :\n  %s\n", best.URL, best.URL, newIssueURL)
	default:
		fmt.Printf("Aucun modem câble reconnu. Si le vôtre est à une autre adresse :\n  mire detecter -modem http://adresse-du-modem\n")
	}
	return nil
}

// applyTimeZone regle l'heure locale sur le fuseau de l'operateur quand TZ
// n'est pas defini : une installation belge affiche l'heure de Bruxelles
// sans rien configurer. Il s'applique au demarrage, avant toute tache.
func applyTimeZone(ctx context.Context, db *sql.DB, cfg config, logger *slog.Logger) {
	if os.Getenv("TZ") != "" {
		return
	}
	st, err := service.Resolve(ctx, db, cfg.Forced)
	if err != nil || st.Profile.TimeZone == "" {
		return
	}
	loc, err := time.LoadLocation(st.Profile.TimeZone)
	if err != nil {
		logger.Warn("fuseau horaire du profil inconnu", "fuseau", st.Profile.TimeZone)
		return
	}
	time.Local = loc
}
