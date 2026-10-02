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

# 3. détail d'un artifact et liens de téléchargement direct
nexus info rel com.acme:ghc-web                                    # versions (builds d'un snapshot regroupés)
nexus info rel com.acme:ghc-web:03.27.10-0                         # infos + liens (un par ligne, cliquables)
nexus info snap com.acme:ghc-web:03.27.10-0-SNAPSHOT --build 43    # un build précis
nexus info rel com.acme:ghc-web:03.27.10-0 --links                 # uniquement les URL (scripts : | ForEach-Object …)
nexus info rel com.acme:ghc-web:03.27.10-0 -o json                 # tout, structuré

# 4. promouvoir un snapshot en release (toujours commencer par --dry-run)
nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --dry-run
nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT                    # → 03.27.10-0, build le plus récent
nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --build 43 --as-version 03.27.10-1
nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --pin com.acme:parent=1.0
nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --with-parent --dry-run   # promeut aussi le(s) parent(s) SNAPSHOT
nexus promote snap rel com.acme:quality-core:1.4.2 --delete-source             # version déjà figée
```

### Ce que fait `info`

Sans version : liste des versions (type, builds, fichiers, date). Avec une version : dépôt, type (release ou snapshot + build), date de publication et auteur si Nexus les donne, taille totale, puis un tableau des fichiers (taille, sha1, date) et **un lien de téléchargement direct par fichier**, écrit en clair sur sa propre ligne (Ctrl+clic dans Windows Terminal, VS Code, iTerm2…). Les liens sont construits depuis l'URL enregistrée par `nexus init`, pas depuis celle que Nexus renvoie (qui peut être interne). Les `.sha1`/`.md5` et `maven-metadata.xml` sont masqués sauf `--all`. Les tailles que la recherche Nexus ne renvoie pas sont lues par requête `HEAD` (jusqu'à 40 fichiers). Un repository privé demandera vos identifiants au clic.

### Ce que fait `promote`

- **Build** : pour un `-SNAPSHOT`, choisit le build horodaté le plus récent (ou `--build 43` / version horodatée explicite). Le dry-run liste les builds disponibles.
- **Renommage** : `ghc-web-03.27.10-0-20260914.070210-43.war` → `ghc-web-03.27.10-0.war` (classifier et extension conservés). Version cible = source sans `-SNAPSHOT`, ou `--as-version`.
- **Pom** : seul le `<version>` du projet est réécrit (mise en forme, commentaires et fins de ligne préservés). Les références `-SNAPSHOT` (parent, dépendances, plugins, propriétés) **bloquent** la promotion ; `--pin groupId:artifactId=version` les réécrit, `--allow-snapshot-refs` passe outre.
- **Binaires** (`.war`, `.jar`…) : copiés **à l'identique** (sha1 inchangé). Leurs métadonnées internes (`META-INF`) peuvent encore mentionner la version SNAPSHOT.
- **Vérification après envoi** : pour chaque fichier, l'outil relit le `.sha1` servi par la destination (en-têtes `no-cache`, 4 tentatives espacées de 1, 2 et 4 s). Si le `.sha1` reste absent ou faux, il **relit le contenu publié** et le compare : un `.sha1` défaillant (délai, cache) ne bloque pas un envoi correct (avertissement), et un contenu faux n'est jamais accepté. L'échec indique les trois valeurs : sha1 attendu, sha1 servi (ou code HTTP), sha1 du contenu publié. Après un échec partiel, la liste des fichiers déjà publiés est affichée ; relancer la commande est sans risque.
- **Empreintes** : `promote` publie lui-même `<fichier>.sha1` et `<fichier>.md5` pour chaque fichier envoyé (marqueur compris), comme `mvn deploy` : sur certains Nexus, un `PUT` simple n'en produit aucune (HTTP 404 sur le `.sha1`). Un fichier déjà présent, identique, mais sans empreintes (cas d'une promotion faite avec une version antérieure de l'outil) n'est pas renvoyé : seules ses empreintes sont ajoutées (« compléter » dans le plan). `maven-metadata.xml` reste généré par Nexus ; l'outil contrôle qu'il liste la version.
- **Traçabilité** : ajoute `ghc-web-03.27.10-0-promoted-from-03.27.10-0-20260914.091709-45.txt` (la version d'origine exacte est dans le nom ; le contenu donne dépôt, version, build source, date, auteur, sha1) **en dernier** : sa présence signifie « promotion complète ». S'il ne peut pas être écrit, la promotion reste valide (avertissement, source conservée) et relancer la commande le réécrit. Un marqueur déjà présent, quelle que soit son origine, n'est jamais dupliqué. `--no-marker` pour le désactiver.
- **Sûreté** : refuse une destination SNAPSHOT/lecture seule/même repo, un fichier existant différent ; reprenable (fichiers identiques ignorés) ; `--delete-source` ne supprime que le build promu, après vérification complète.

Codes retour : 0 ok · 1 erreur · 2 usage · 3 partiel · 4 accès refusé · 130 interrompu.

Configuration dans `~/.nexus/` (`NEXUS_HOME` pour la déplacer). Pour l'automatisation :
`NEXUS_<ALIAS>_USER` / `NEXUS_<ALIAS>_PASSWORD` (alias en majuscules, `-` → `_`).

## Build hors-ligne / réseau d'entreprise

Les dépendances sont **vendorisées** (`vendor/`) : `go build` et `go test` n'ont besoin d'aucun accès à `proxy.golang.org`.

```sh
go test ./...
go build -o nexus.exe .        # nexus sous Linux/macOS
```

Pour mettre à jour une dépendance (depuis un poste avec accès réseau) :
`go get <module>@<version> && go mod tidy && go mod vendor`, puis committer `go.mod`, `go.sum` et `vendor/`.

## Documentation

- [docs/CLI-UX-GUIDELINES.md](docs/CLI-UX-GUIDELINES.md) — charte UX commune à tous nos CLI (couleurs, tableaux, flux, pagination, erreurs, codes retour)
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — modules, promotion, trajectoire MCP

## Développement

```sh
go vet ./... && go test ./...
```
