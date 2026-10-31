# Contribuer à Mire

Contributions les plus utiles : un nouveau modem, un nouvel opérateur. Les deux reposent sur des relevés réels.

## Ajouter un modem

### 1. Rassembler des relevés

Dans Mire, choisissez **« Autre modem, non pris en charge »**, puis **Proposer mon modem** dans la page Modem (ou `mire diagnostic -modem http://adresse`). Le rapport lit la page d'accueil et l'interface du modem, sans rien modifier. Il est anonymisé : adresses MAC, numéro de série, noms Wi-Fi et d'appareils, adresses IP publiques, jetons. La page ouvre ensuite une demande GitHub déjà remplie.

Plusieurs rapports aident : ligne saine, ligne dégradée, modem qui vient de redémarrer.

### 2. Écrire le pilote

Fichier `internal/modem/<modele>.go`, qui implémente :

```go
type Driver interface {
	Snapshot(ctx context.Context) (docsis.RawSnapshot, error)
}
```

`Snapshot` se connecte si besoin, lit les tables et les traduit en `docsis.RawSnapshot`.

| Champ | Format |
|---|---|
| `DOCSIS` | `"3.0"` ou `"3.1"` |
| `Frequency` | `"650 MHz"` |
| `Modulation` | `"256QAM"`, `"OFDM"` (descendant), `"64QAM"`, `"OFDMA"` (montant) |
| `PowerLevel` | dBmV, par exemple `"1.5"` |
| `MSE` | SNR en négatif : `"-38.9"` pour 38,9 dB |
| `CorrErrors`, `NonCorrErrors` | compteurs cumulés depuis le démarrage du modem |
| `Multiplex` | canal OFDMA : modulation du profil actif (`"256QAM"`) |

Une valeur absente reste vide, jamais `0`.

Règles :
- délais d'attente explicites ;
- une seule reconnexion quand la session a expiré ;
- aucun mot de passe dans les journaux ni dans les erreurs ;
- `ErrAuth` pour un refus d'authentification, distinct d'une panne réseau.

Modèle : `cga4233.go`.

#### Gestion du modem (facultatif)

Interfaces de `internal/modem/manage.go` :

| Interface | Méthode | Rôle |
|---|---|---|
| `InfoReader` | `Info(ctx) (Info, error)` | modèle, versions, durée de fonctionnement, mode |
| `RouterReader` | `Router(ctx) (Router, error)` | réseau local, Wi-Fi, appareils, en lecture seule |
| `Restarter` | `Restart(ctx) error` | redémarrage ; `ErrNoAnswer` sans réponse, `ErrRestartRefused` si refusé |
| `Capturer` | `Capture(ctx, progress) ([]Capture, error)` | lectures du rapport, en GET uniquement, passées par `Anonymize` |

Beaucoup de firmwares mêlent redémarrage et réinitialisation d'usine sur le même écran. Un test doit vérifier que la requête de redémarrage ne contient que ses propres champs.

Le pilote sert à la fois la collecte et la page : ses méthodes doivent supporter des appels concurrents. Aucune écriture sur le modem sans demande confirmée.

### 3. Tester

Les réponses anonymisées deviennent des cas de test (`internal/modem/<modele>_test.go`) : chaque table lue, les valeurs absentes, les variantes de format.

### 4. Inscrire le modèle

Dans `internal/modem/registry.go` :

```go
{
	ID:          "cm1000",              // valeur de MIRE_MODELE
	Name:        "Netgear CM1000",
	Firmware:    "",                    // variante validée, si elle compte
	DefaultUser: "admin",
	Markers:     []string{"cm1000"},    // fragments propres à la page d'accueil, en minuscules
	New: func(c Config) (Driver, error) { return NewCM1000(c.URL, c.User, c.Password) },
},
```

## Ajouter un opérateur

Ouvrez une demande **« Nouvel opérateur »**, ou proposez un fichier `internal/operator/profiles/<id>.json` :

- `default_modem_url` : adresse du modem, souvent `http://192.168.100.1` ;
- `default_modem` : modem fourni par l'opérateur ;
- `calibrated` : `true` si les seuils viennent de sources vérifiables ; sinon, la ligne n'est pas jugée ;
- `source`, `notes` : origine et limites des seuils ;
- `thresholds` : puissance par modulation, SNR minimal, modulation montante, erreurs.

| Champ `errors` | Rôle | Défaut |
|---|---|---|
| `uncorrectable_pct_warning`, `uncorrectable_pct_critical` | part d'erreurs non corrigées : à surveiller, critique | — |
| `window_hours` | fenêtre du jugement | 24 |
| `min_codewords` | volume minimal pour juger la proportion | 1 000 |
| `spike_uncorrectable` | erreurs non corrigées entre deux relevés qui ouvrent un pic | 1 000 |

Modèle : `voo.json`.

Pour vérifier des seuils sur une ligne réelle : demande **« Relevés pour le calibrage »**, avec l'archive anonymisée de la page Rapport.

## Ajouter un réglage

Déclaration unique dans `internal/settings/groups.go` : clé, variable, type, défaut, libellé, aide, validation. La page, l'environnement et le verrouillage en découlent.

- `Toggle` d'un groupe : interrupteur principal de la rubrique.
- `Country` d'un groupe : rubrique réservée aux opérateurs de ce pays.
- `Select` avec `Options` : liste déroulante.
- `Only: "cle=valeur"` : champ affiché selon une liste.
- Un champ vidé revient à sa valeur par défaut ; un mot de passe vidé est conservé.

## Ajouter un service de notification

1. Ses champs dans le groupe `notifications` (`Only: NotifyService + "=<service>"`) et le service dans `NotifyService`.
2. Un expéditeur dans `internal/notify/senders.go` (`Send(ctx, Message) error`) et son cas dans `New`.
3. Un test avec `httptest`.

Une erreur ne reprend jamais une adresse de webhook ni un jeton : passez par `post`.

## Vérifier

```bash
gofmt -l .                   # ne doit rien afficher
go vet ./...
go test -race ./...
go test -tags purego ./...   # pilote SQLite des binaires publiés
```

Une faille de sécurité se signale en privé : voir [SECURITY.md](SECURITY.md).

## Publier une version

Depuis `main`, CI verte, poussez un tag :

```bash
git tag v1.2.0
git push origin v1.2.0
```

Le tag crée la release (binaires, notes tirées du message du commit) et l'image `1.2.0`, `1.2` et `latest`. Un tag à suffixe (`v1.3.0-rc1`) publie une pré-version. Ne créez pas la release depuis GitHub : publiée sans ses binaires, elle ne peut plus les recevoir.

## Interface et textes

Voir [docs/charte.md](docs/charte.md).
