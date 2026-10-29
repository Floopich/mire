# Sécurité

Mire garde le mot de passe de votre modem et l'histoire de votre ligne : une faille compte.

## Signaler une faille

Ne l'exposez pas dans une issue publique. Utilisez le signalement privé de GitHub : onglet **Security**, puis **Report a vulnerability**. Décrivez la version concernée (`mire version`), le comportement constaté et, si possible, comment le reproduire.

Vous recevez une réponse sous quelques jours. Une correction confirmée est publiée dans une nouvelle version, et le signalement est crédité si vous le souhaitez.

## Versions suivies

Seule la dernière version publiée reçoit les corrections de sécurité : mettez à jour avec `docker compose pull && docker compose up -d`, ou en remplaçant le binaire.

## Ce que Mire protège

Les choix de sécurité de Mire, et ce qui sort du réseau local, sont décrits dans [docs/configuration.md](docs/configuration.md#sécurité).
