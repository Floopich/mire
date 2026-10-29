## Ce que change cette proposition

<!-- Le problème réglé ou la fonction ajoutée, en quelques phrases. -->

## Vérifications

- [ ] `gofmt -l .` ne liste rien, `go vet ./...` passe
- [ ] `go test ./...` et `go test -tags purego ./...` passent
- [ ] Les textes de l'interface suivent `docs/charte.md` : français courant, sans jargon
- [ ] Un réglage, une variable ou une commande ajoutés sont décrits dans `docs/` (et `.env.exemple` pour une variable)
- [ ] Les relevés de modem joints sont anonymisés : ni adresse MAC, ni numéro de série, ni mot de passe
