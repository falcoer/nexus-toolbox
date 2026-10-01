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
3. **copie** : binaires streamés via fichier temporaire (sha1 recalculé et comparé à la source), pom envoyé depuis la mémoire, `PUT` sur le chemin Maven de la destination (Nexus génère checksums et métadonnées) ;
4. **vérification** : sha1 servi par la destination = sha1 attendu ; lecture de `maven-metadata.xml` ;
5. **marqueur** `-promoted-from.txt` (`marker.go`), puis `--delete-source` du composant promu si tout est vérifié.

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
