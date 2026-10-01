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

# 3. promouvoir un snapshot en release (toujours commencer par --dry-run)
nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --dry-run
nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT                    # → 03.27.10-0, build le plus récent
nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --build 43 --as-version 03.27.10-1
nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --pin com.acme:parent=1.0
nexus promote snap rel com.acme:quality-core:1.4.2 --delete-source             # version déjà figée
```

### Ce que fait `promote`

- **Build** : pour un `-SNAPSHOT`, choisit le build horodaté le plus récent (ou `--build 43` / version horodatée explicite). Le dry-run liste les builds disponibles.
- **Renommage** : `ghc-web-03.27.10-0-20260914.070210-43.war` → `ghc-web-03.27.10-0.war` (classifier et extension conservés). Version cible = source sans `-SNAPSHOT`, ou `--as-version`.
- **Pom** : seul le `<version>` du projet est réécrit (mise en forme, commentaires et fins de ligne préservés). Les références `-SNAPSHOT` (parent, dépendances, plugins, propriétés) **bloquent** la promotion ; `--pin groupId:artifactId=version` les réécrit, `--allow-snapshot-refs` passe outre.
- **Binaires** (`.war`, `.jar`…) : copiés **à l'identique** (sha1 inchangé). Leurs métadonnées internes (`META-INF`) peuvent encore mentionner la version SNAPSHOT.
- **Empreintes** : `.sha1`/`.md5` ne sont jamais envoyés, Nexus les génère ; `promote` relit le sha1 servi par la destination pour vérifier chaque fichier, puis contrôle que `maven-metadata.xml` liste la version.
- **Traçabilité** : ajoute `ghc-web-03.27.10-0-promoted-from.txt` (dépôt/version/build source, date, auteur, sha1) **en dernier** : sa présence signifie « promotion complète ». `--no-marker` pour le désactiver.
- **Sûreté** : refuse une destination SNAPSHOT/lecture seule/même repo, un fichier existant différent ; reprenable (fichiers identiques ignorés) ; `--delete-source` ne supprime que le build promu, après vérification complète.

Codes retour : 0 ok · 1 erreur · 2 usage · 3 partiel · 4 accès refusé · 130 interrompu.

Configuration dans `~/.nexus/` (`NEXUS_HOME` pour la déplacer). Pour l'automatisation :
`NEXUS_<ALIAS>_USER` / `NEXUS_<ALIAS>_PASSWORD` (alias en majuscules, `-` → `_`).

## Documentation

- [docs/CLI-UX-GUIDELINES.md](docs/CLI-UX-GUIDELINES.md) — charte UX commune à tous nos CLI (couleurs, tableaux, flux, pagination, erreurs, codes retour)
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — modules, promotion, trajectoire MCP

## Développement

```sh
go vet ./... && go test ./...
```
