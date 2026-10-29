# Contribuer à Mire

Mire se veut utilisable par n'importe quel abonné câble. Les deux contributions les plus utiles sont **un nouveau modem** et **un nouvel opérateur**. Les deux reposent sur la même règle : rien n'entre sans relevés réels.

## Ajouter un modem

### 1. Rassembler des relevés réels

Le plus simple : dans Mire, choisir **« Autre modem, non pris en charge »**, puis **Proposer mon modem** sur la page Modem (ou `mire diagnostic -modem http://adresse` en ligne de commande). Mire lit, sans rien modifier et sans identifiants, la page d'accueil du modem et les écrans de son interface web : scripts, gabarits, menu, avec ce qui entoure les mots *reboot*, *restart*, *reset*, *docsis* et *downstream*. Pour un modèle déjà pris en charge, il y ajoute chaque lecture connue du pilote, une fois connecté, et l'écran de redémarrage en entier. Tout est anonymisé : adresses MAC, numéro de série, noms de réseau Wi-Fi et d'appareils, adresses IP publiques, jetons.

L'abonné nomme son modem, relit le résumé, puis ouvre une demande GitHub déjà remplie. Le rapport complet, en texte lisible, se télécharge à part : une ligne par élément de chaque table, à joindre si un mainteneur le demande.

Pour écrire un pilote, plusieurs rapports pris à des moments différents aident : ligne saine, ligne dégradée, modem qui vient de redémarrer. Une réponse partagée à la main doit être anonymisée de la même façon.

### 2. Écrire le pilote

Un pilote est un fichier `internal/modem/<modele>.go` qui implémente :

```go
type Driver interface {
	Snapshot(ctx context.Context) (docsis.RawSnapshot, error)
}
```

`Snapshot` se connecte si besoin, lit les tables et les traduit en `docsis.RawSnapshot`. Le reste du programme (collecte, stockage, analyse, interface) ne connaît que cette structure.

Conventions de `RawSnapshot` :

| Champ | Format attendu |
|---|---|
| `DOCSIS` | `"3.0"` ou `"3.1"` |
| `Frequency` | `"650 MHz"`, en MHz entiers |
| `Modulation` | `"256QAM"`, `"OFDM"` en voie descendante, `"64QAM"`, `"OFDMA"` en voie montante |
| `PowerLevel` | puissance en dBmV, par exemple `"1.5"` |
| `MSE` | rapport signal sur bruit noté en négatif, par exemple `"-38.9"` pour 38,9 dB |
| `CorrErrors`, `NonCorrErrors` | compteurs cumulés depuis le démarrage du modem |
| `Multiplex` | pour un canal OFDMA, l'ordre de modulation du profil actif (`"256QAM"`) |

Une valeur que le modem ne donne pas reste **vide**, jamais `0` : un zéro serait jugé comme une vraie mesure.

Règles de comportement :
- délais d'attente explicites, sans jamais bloquer la collecte ;
- une seule reconnexion quand la session a expiré, pas de boucle ;
- le mot de passe ne figure ni dans les journaux ni dans les erreurs ;
- les erreurs distinguent un refus d'authentification (`ErrAuth`) d'une panne réseau.

Le pilote `cga4233.go` sert de modèle.

#### Gestion du modem, facultative

La page Modem propose ce que le pilote sait faire. Chaque capacité est une interface distincte (`internal/modem/manage.go`), à n'implémenter qu'avec des réponses réelles :

| Interface | Méthode | Rôle |
|---|---|---|
| `InfoReader` | `Info(ctx) (Info, error)` | modèle, versions, durée de fonctionnement, mode pont ou routeur |
| `RouterReader` | `Router(ctx) (Router, error)` | réseau local, Wi-Fi, appareils, en lecture seule ; une partie illisible est nommée dans `Missing` |
| `Restarter` | `Restart(ctx) error` | redémarrage, exactement l'appel du bouton de l'interface du modem relevé dans un rapport ; `ErrNoAnswer` si la demande est partie sans réponse, `ErrRestartRefused` si le modem l'a refusée |
| `Capturer` | `Capture(ctx, progress) ([]Capture, error)` | lectures du rapport, uniquement des GET, passées par `Anonymize` ; `progress` nomme chaque étape pour la page de suivi |

Sur un même écran, beaucoup de firmwares mêlent redémarrage et réinitialisation d'usine (chez VOO : `reboot=…` contre `reset=…` vers le même chemin). Un test doit vérifier que le formulaire envoyé ne contient que les champs du redémarrage.

Le modem n'accorde souvent qu'une session : le même pilote sert la collecte et la page, ses méthodes doivent donc pouvoir être appelées depuis plusieurs goroutines (un `sync.Mutex` suffit). Aucune écriture sur le modem sans une demande confirmée de l'utilisateur.

### 3. Tester avec les relevés réels

Les réponses anonymisées deviennent des cas de test (`internal/modem/<modele>_test.go`). Ils doivent couvrir chaque table lue, les valeurs absentes, et les variantes de format rencontrées (nombre ou texte, Hz ou MHz).

### 4. Inscrire le modèle

Ajoutez une entrée dans `internal/modem/registry.go` :

```go
{
	ID:          "cm1000",              // valeur de MIRE_MODELE
	Name:        "Netgear CM1000",      // nom sur l'étiquette
	Firmware:    "",                    // variante validée, si elle compte
	DefaultUser: "admin",
	Markers:     []string{"cm1000"},    // fragments de la page d'accueil, en minuscules
	New: func(c Config) (Driver, error) { return NewCM1000(c.URL, c.User, c.Password) },
},
```

Le modèle apparaît alors dans `mire modeles` et dans la liste de la page Réglages, se choisit aussi avec `MIRE_MODELE`, et `mire detecter` le reconnaît grâce à ses repères (`Markers`) : des fragments propres à sa page d'accueil, jamais un mot générique comme « docsis ».

## Ajouter un opérateur

Pour proposer les seuils d'un opérateur sans écrire de code, ouvrez une demande **« Nouvel opérateur »** : elle rassemble ce qu'il faut pour écrire le profil.

Un opérateur est un fichier `internal/operator/profiles/<id>.json`, sans code :

- `default_modem_url` : adresse de diagnostic du modem, souvent `http://192.168.100.1` ;
- `default_modem` : identifiant du modem fourni par l'opérateur ;
- `calibrated` : `true` seulement si les seuils viennent de sources vérifiables ;
- `source` et `notes` : d'où viennent les seuils et ce qu'ils ne couvrent pas ;
- `thresholds` : plages de puissance par modulation, planchers de SNR, limites de modulation montante et d'erreurs.

Le bloc `errors` règle le jugement des erreurs, toujours calculé sur l'accroissement des compteurs et jamais sur leur cumul :

| Champ | Rôle | Défaut |
|---|---|---|
| `uncorrectable_pct_warning`, `uncorrectable_pct_critical` | part d'erreurs non corrigées qui rend un canal à surveiller, puis critique | — |
| `window_hours` | fenêtre glissante du jugement | 24 |
| `min_codewords` | nombre d'erreurs en dessous duquel la proportion n'est pas jugée | 1 000 |
| `spike_uncorrectable` | erreurs non corrigées entre deux relevés, sur toute la ligne, qui ouvrent un pic au journal | 1 000 |

Un profil non calibré conserve les relevés mais ne juge pas la ligne : mieux vaut aucun verdict qu'un verdict fondé sur les tolérances d'un autre réseau. Le profil `voo.json` sert de modèle.

## Ajouter un réglage

Un réglage se déclare une seule fois, dans `internal/settings/groups.go` : clé, variable d'environnement, type, valeur par défaut, libellé, aide et validation. La page Réglages, la lecture de l'environnement, la priorité (variable > valeur enregistrée > défaut) et le verrouillage en découlent sans autre code.

- Les groupes sont les rubriques de la page Réglages, rangées par tâche (modem, collecte, analyse…) : un réglage rejoint le groupe de la tâche qu'il sert. Le résumé d'une rubrique se calcule dans `internal/webview/reglages.go`.
- Type `Bool` désigné comme `Toggle` d'un groupe : l'interrupteur principal de la rubrique, qui active l'option et dévoile ses champs.
- `Country` d'un groupe : la rubrique n'apparaît que pour un opérateur de ce pays.
- Type `Select` avec `Options` : une liste déroulante.
- Champ `Only: "cle=valeur"` : le champ n'apparaît que si la liste `cle` vaut `valeur`, par exemple les champs propres à un service de notification.
- Un champ vidé revient à sa valeur par défaut ; un mot de passe vidé garde l'ancien.

Le code lit la valeur effective par `service.State().Values` et réagit au rechargement que déclenche chaque enregistrement.

## Ajouter un service de notification

1. Ses champs dans le groupe `notifications` de `internal/settings/groups.go`, chacun avec `Only: NotifyService + "=<service>"`, et le service dans la liste `NotifyService`.
2. Un expéditeur dans `internal/notify/senders.go`, qui implémente `Send(ctx, Message) error`, et son cas dans `New`.
3. Un test contre un serveur local (`httptest`), qui vérifie le format reçu.

Une erreur ne doit jamais reprendre une adresse de webhook ni un jeton : passez par `post`, qui les retire.

## Vérifier avant de proposer

Le modèle de pull request reprend ces vérifications sous forme de cases à cocher. Une faille de sécurité se signale en privé : voir [SECURITY.md](SECURITY.md).

```bash
gofmt -l .                   # doit ne rien afficher
go vet ./...
go test -race ./...          # pilote SQLite cgo
go test -tags purego ./...   # pilote Go pur, celui des binaires publiés
```

La CI refait ces étapes et construit l'image pour amd64, arm64 et armv7.

## Publier une version

Les versions suivent la numérotation sémantique. Depuis `main` à jour, CI verte :

```bash
git tag -a v0.2.0 -m "Mire 0.2.0"
git push origin v0.2.0
```

Ou depuis GitHub : **Releases**, « Draft a new release », nouveau tag `v0.2.0` sur `main`, « Generate release notes », puis « Publish release ».

Le tag publie les binaires dans les Releases et l'image `0.2.0`, `0.2` et `latest`. Un tag à suffixe (`v0.3.0-rc1`) publie une pré-version, sans toucher à `latest`. Un tag d'une autre forme (`v.0.2.0`, `v0.2`) ne publie rien.

## Interface et textes

L'interface suit `docs/charte.md` : Material 3 Expressive, jetons de couleur, aucun script ni ressource externe, français courant. Un texte nouveau suit le vocabulaire de la charte.
