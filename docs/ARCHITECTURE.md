# Architecture

## Vue d'ensemble

```
cmd (cobra)  ─┐
              ├─►  internal/module (actions)  ─►  internal/nexus (client REST)
mcp (futur)  ─┘          │                        internal/formats/<maven|docker|…>
                         └─► retourne des données, jamais d'affichage
internal/ui  (rendu terminal, CLI uniquement)
```

Règle structurante : **une action est une méthode pure** `func(ctx, Target, Input) (Output, error)`,
Input/Output sont des structs sérialisables en JSON (voir `internal/module/module.go`). Le CLI les habille via `internal/ui`.
Un futur serveur MCP les exposera tels quels. Aucun `fmt.Print` hors `internal/ui` et `cmd`.
Les retours d'avancement passent par un `Reporter` injecté (le CLI affiche une barre, MCP émettrait des notifications).

## Modules

Un **module** correspond à un format de repository Nexus (`maven2`, `docker`, `pypi`, `npm`…).
Il déclare les **actions** qu'il supporte.

```go
type Module interface{ Format() string }

// Capacités optionnelles : un module implémente celles qu'il supporte.
type Searcher interface { Search(ctx, Target, SearchInput) (Iterator[Hit], error) }
type Inspector interface {
    Versions(ctx, Target, group, artifact string) (*ArtifactSummary, error)
    Inspect(ctx, Target, InspectInput) (*ComponentDetails, error)  // une version/un build, un lien direct par fichier
}
type Promoter interface {
    PlanPromote(ctx, src, dst Target, in PromoteInput) (*PromotePlan, error)
    ExecutePromote(ctx, src, dst Target, plan *PromotePlan, rep Reporter) (*PromoteResult, error)
}
```

`module.Capabilities(m)` liste les actions d'un module (affichées par `nexus repos list`).
Les actions d'écriture sont en deux temps (plan puis exécution) : le plan sert au `--dry-run`,
à la confirmation et, plus tard, à la validation côté MCP.

Le registre (`internal/module`) mappe le format renvoyé par `GET /service/rest/v1/repositories`
vers son module. `nexus init` stocke le format ; les commandes génériques (`search`, `promote`, …)
dispatchent ensuite. Une action absente du module donne une erreur explicite.

Ajouter un format = créer `internal/formats/<format>/` et l'ajouter à `module.NewRegistry(...)` dans `cmd/root.go`.

## Configuration et secrets

- `~/.nexus/config.yaml` : alias → url, base, repository, format, type, user (aucun secret).
- Secrets : trousseau de l'OS (`nexus-toolbox/<alias>`) ; fallback `~/.nexus/credentials` en `0600` avec avertissement ;
  surcharge par `NEXUS_<ALIAS>_USER` / `NEXUS_<ALIAS>_PASSWORD` (alias en majuscules, `-` → `_`).
- Le répertoire peut être déplacé avec `NEXUS_HOME`.

## Promotion Maven

Nexus 3 OSS ne propose pas d'API de promotion (staging = Pro). `promote` = **renommage + copie vérifiée**
(+ suppression optionnelle de la source). Voir `internal/formats/maven/` :

1. **résolution** (`snapshot.go`) : pour un `-SNAPSHOT`, recherche `maven.baseVersion`, regroupement des fichiers par build horodaté (`-YYYYMMDD.HHMMSS-N`), choix du plus récent ou de `--build` ; pour une version figée, composant exact ;
2. **plan** (`promote.go`, aucune écriture) : validations (hosted, version/write policy de la destination), chemin de destination de chaque fichier, pom réécrit en mémoire (`pom.go`, découpe aux offsets), sha1 attendu, détection fichier par fichier via `GET <dst>/<path>.sha1` (absent → copier, identique → ignorer, différent → conflit), références SNAPSHOT ;
3. **copie** : binaires streamés via fichier temporaire (sha1 recalculé et comparé à la source), pom envoyé depuis la mémoire, `PUT` sur le chemin Maven de la destination, puis `PUT` de `<fichier>.sha1` et `<fichier>.md5` (certains Nexus ne servent aucune empreinte pour un `PUT` simple) ; `maven-metadata.xml` est généré par Nexus ; un fichier identique sans empreintes est « complété » sans être renvoyé ;
4. **vérification** (`verify.go`) : sha1 servi par la destination = sha1 attendu (relectures `no-cache` avec back-off), sinon hash du contenu publié ; échec = les trois valeurs ; lecture de `maven-metadata.xml` ;
5. **marqueur** `-promoted-from-<version d'origine>.txt` (`marker.go` ; un marqueur existant est conservé ; si des fichiers changent, un marqueur de la nouvelle origine s'ajoute (historique) ; son échec n'invalide pas la promotion), puis `--delete-source` du composant promu si tout est vérifié.

**Modules requis** (`closure.go`) : avec `PromoteInput.WithParent` (toujours actif pour la forme courte et l'assistant), `rewritePomItem` lit les `Need` du pom (`PomResult.Needs` : parent, dépendances/plugins en `-SNAPSHOT`, propriétés `-SNAPSHOT` reliées à un artifact via `PropUsers`). Pour chacun, `resolveNeed` calcule la version cible (pin, sinon version de la racine si même base, sinon base sans `-SNAPSHOT`) puis le statut : `released` (déjà dans la destination), `promote` (sous-plan via `PlanPromote`, récursif, état partagé `ChainState` : un module n'est planifié qu'une fois, cycles coupés, plafond de 100 modules par chaîne) ou `blocked` (`ErrNoBuild` et absent de la release : `PromotePlan.Blockers`, avec les versions existantes). Le pom est réécrit avec ces versions (pins pour parent/dépendances, `PomOpts.Targets` pour les propriétés). `PromotePlan.Parents` est la liste ordonnée (dépendances d'abord) exécutée avant l'artifact racine ; `ExecutePromote` refuse tout s'il y a un bloquant, un conflit ou une référence SNAPSHOT.

**Couche CLI** (`cmd/promote*.go`) : trois entrées (forme complète, forme courte, assistant) produisent un `promoteReq` que `runPromote` planifie, présente (`printPromotionPlan`), valide (bloquants → erreur ; modules supplémentaires → confirmation stricte au terminal, sinon `planRequiredError` = code 5, sauf `--with-deps`) et exécute ; chaque issue affiche la commande courte équivalente. Dépôts par défaut : `config.PromotionPair`.

Propriétés de version SNAPSHOT : `PomOpts{Set, Aligned, StripSnapshot}` (priorité dans cet ordre) ; `PomResult.PropUsers` associe chaque propriété aux coordonnées qui l'utilisent comme version, ce qui permet de vérifier leur présence dans la destination. Les avertissements « pin / set-property sans effet » sont calculés une fois pour toute la chaîne (`finalizeUsage`).

Relancer la commande reprend là où elle s'est arrêtée (fichiers identiques ignorés, marqueur existant conservé).

## Trajectoire MCP (non implémentée)

`nexus serve --mcp` (stdio, puis HTTP) enregistrera chaque `Action` comme *tool*, avec un JSON Schema
dérivé de la struct d'entrée. Garde-fous prévus : actions d'écriture en dry-run par défaut ou
exigeant `confirm: true`, allowlist d'actions/repos, aucune saisie interactive, mêmes secrets.

## Jalons

1. Socle + search + promote Maven (courant)
2. Maven `analyze` et `clean`
3. Modules docker / npm / pypi
4. Serveur MCP
