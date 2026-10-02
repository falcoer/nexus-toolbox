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

# 4. télécharger
nexus download rel com.acme:ghc-web:03.27.10-0                          # dans le dossier courant
nexus download rel com.acme:ghc-web:03.27.10-0 --dir .\dist --include "*.zip"
nexus download snap com.acme:ghc-web:03.27.10-0-SNAPSHOT --build 43 --dry-run

# 5. promouvoir (artifact, puis version cible ; tout le reste est déduit)
nexus promote flux-editor 18.00.00.beta1-0 --dry-run             # le plan, rien n'est écrit
nexus promote flux-editor 18.00.00.beta1-0                       # promotion (confirmation demandée)
nexus promote                                                    # assistant interactif, affiche la commande à réutiliser
```

### Ce que fait `info`

Sans version : liste des versions (type, builds, fichiers, date). Avec une version : dépôt, type (release ou snapshot + build), date de publication et auteur si Nexus les donne, taille totale, puis un tableau des fichiers (taille, sha1, date) et **un lien de téléchargement direct par fichier**, écrit en clair sur sa propre ligne (Ctrl+clic dans Windows Terminal, VS Code, iTerm2…). Les liens sont construits depuis l'URL enregistrée par `nexus init`, pas depuis celle que Nexus renvoie (qui peut être interne). Les `.sha1`/`.md5` et `maven-metadata.xml` sont masqués sauf `--all` : la recherche Nexus ne les liste pas, `--all` les cherche donc à côté de chaque fichier (un seul `maven-metadata.xml` est retourné, celui du dossier de la version, sinon celui de l'artifact). `nexus download --all` les récupère aussi. Les tailles que la recherche Nexus ne renvoie pas sont lues par requête `HEAD` (jusqu'à 40 fichiers). Un repository privé demandera vos identifiants au clic.

### Ce que fait `download`

Télécharge les fichiers d'une version (ceux de `nexus info`) : chacun est écrit en `.part`, comparé au sha1 donné par Nexus, puis renommé, donc un transfert interrompu ou corrompu ne laisse jamais un fichier qui paraît complet. Un fichier local identique est ignoré ; un fichier différent est refusé sauf `--force`. `--include` / `--exclude` filtrent par nom (motifs `*.zip`), `--dry-run` liste sans rien écrire, `--build` choisit un build de snapshot. Les chemins téléchargés sont écrits sur la sortie standard, un par ligne ; relancer la commande après une coupure ne retélécharge que ce qui manque.

### Ce que fait `promote` (voir [docs/PROMOTION.md](docs/PROMOTION.md))

Aide-mémoire complet : les 4 règles automatiques (dépôts, artifact, source, cible), les statuts des modules requis (« en release », « à promouvoir », « bloquant ») et le code retour 5. Détail des mécanismes :


- **Build** : pour un `-SNAPSHOT`, choisit le build horodaté le plus récent (ou `--build 43` / version horodatée explicite). Le dry-run liste les builds disponibles.
- **Renommage** : `ghc-web-03.27.10-0-20260914.070210-43.war` → `ghc-web-03.27.10-0.war` (classifier et extension conservés). Version cible = source sans `-SNAPSHOT`, ou `--as-version`.
- **Pom** : seul le `<version>` du projet est réécrit (mise en forme, commentaires et fins de ligne préservés). Les références `-SNAPSHOT` (parent, dépendances, plugins, propriétés) **bloquent** la promotion ; `--pin groupId:artifactId=version` les réécrit, `--allow-snapshot-refs` passe outre.
- **Binaires** (`.war`, `.jar`…) : copiés **à l'identique** (sha1 inchangé). Leurs métadonnées internes (`META-INF`) peuvent encore mentionner la version SNAPSHOT.
- **Vérification après envoi** : pour chaque fichier, l'outil relit le `.sha1` servi par la destination (en-têtes `no-cache`, 4 tentatives espacées de 1, 2 et 4 s). Si le `.sha1` reste absent ou faux, il **relit le contenu publié** et le compare : un `.sha1` défaillant (délai, cache) ne bloque pas un envoi correct (avertissement), et un contenu faux n'est jamais accepté. L'échec indique les trois valeurs : sha1 attendu, sha1 servi (ou code HTTP), sha1 du contenu publié. Après un échec partiel, la liste des fichiers déjà publiés est affichée ; relancer la commande est sans risque.
- **Empreintes** : `promote` publie lui-même `<fichier>.sha1` et `<fichier>.md5` pour chaque fichier envoyé (marqueur compris), comme `mvn deploy` : sur certains Nexus, un `PUT` simple n'en produit aucune (HTTP 404 sur le `.sha1`). Un fichier déjà présent, identique, mais sans empreintes (cas d'une promotion faite avec une version antérieure de l'outil) n'est pas renvoyé : seules ses empreintes sont ajoutées (« compléter » dans le plan). `maven-metadata.xml` reste généré par Nexus ; l'outil contrôle qu'il liste la version.
- **Traçabilité** : ajoute `ghc-web-03.27.10-0-promoted-from-03.27.10-0-20260914.091709-45.txt` (la version d'origine exacte est dans le nom ; le contenu donne dépôt, version, build source, date, auteur, sha1) **en dernier** : sa présence signifie « promotion complète ». S'il ne peut pas être écrit, la promotion reste valide (avertissement, source conservée) et relancer la commande le réécrit. Un marqueur de la même origine est conservé ; si la promotion modifie des fichiers déjà publiés (autre build, `--force`), un nouveau marqueur est ajouté à côté de l'ancien : le plus récent (`promoted-at`) fait foi, les autres restent en historique. `--no-marker` pour le désactiver.
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
- [docs/PROMOTION.md](docs/PROMOTION.md) — aide-mémoire de la promotion (à lire avant de réutiliser l'outil)
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — modules, promotion, trajectoire MCP

## Développement

```sh
go vet ./... && go test ./...
```
