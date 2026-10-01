# nexus-toolbox

CLI `nexus` pour Nexus Repository 3, dans l'esprit de `gh` : on enregistre des repositories
sous un alias, puis on agit dessus (recherche, promotion, et bientôt analyse / nettoyage).
Les actions sont des **modules par format** (maven2 aujourd'hui ; docker, npm, pypi… ensuite)
et un **mode serveur MCP** est prévu (voir [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)).

## Installation

```sh
go build -ldflags "-X github.com/falcoer/nexus-toolbox/cmd.version=0.1.0" -o nexus .
```

## Utilisation

```sh
# 1. enregistrer des repositories (login/mot de passe demandés, stockés dans le trousseau de l'OS)
nexus init rdsf-qp-snapshots https://nexus.example.com/nexus/repository/rdsf-qp-maven-snapshots/
nexus init rdsf-qp-releases  https://nexus.example.com/nexus/repository/rdsf-qp-maven-releases/
nexus repos list

# 2. chercher
nexus search rdsf-qp-snapshots quality --from-version 1.2 --to-version 1.9
nexus search rdsf-qp-snapshots --group com.acme -o json

# 3. promouvoir un artifact (toujours commencer par --dry-run)
nexus promote rdsf-qp-snapshots rdsf-qp-releases com.acme:quality-core:1.4.2 --dry-run
nexus promote rdsf-qp-snapshots rdsf-qp-releases com.acme:quality-core:1.4.2 --delete-source
```

`promote` : copie vérifiée (sha1 côté destination), reprenable, refuse les `-SNAPSHOT`, respecte la
write policy de la destination. Codes retour : 0 ok · 1 erreur · 2 usage · 3 partiel · 4 accès refusé · 130 interrompu.

Configuration dans `~/.nexus/` (`NEXUS_HOME` pour la déplacer). Pour l'automatisation :
`NEXUS_<ALIAS>_USER` / `NEXUS_<ALIAS>_PASSWORD` (alias en majuscules, `-` → `_`).

## Documentation

- [docs/CLI-UX-GUIDELINES.md](docs/CLI-UX-GUIDELINES.md) — charte UX commune à tous nos CLI (couleurs, tableaux, flux, pagination, erreurs, codes retour)
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — modules, promotion, trajectoire MCP

## Développement

```sh
go vet ./... && go test ./...
```
