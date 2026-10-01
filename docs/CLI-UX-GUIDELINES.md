# Charte UX des outils CLI

Document de référence **indépendant de Nexus et du langage** : il s'applique à tous les
outils en ligne de commande que nous construisons, afin que `nexus`, et les futurs outils,
partagent les mêmes réflexes visuels et les mêmes conventions de script.

Mots-clés : **DOIT** = obligatoire, **DEVRAIT** = fortement recommandé, **PEUT** = optionnel.

---

## 1. Principes

1. **Humain d'abord, script ensuite.** Sur un terminal interactif la sortie est belle et guidée ; branchée sur un pipe ou un fichier, elle devient brute, stable et sans décoration.
2. **stdout = données, stderr = dialogue.** Résultats sur stdout ; spinners, avertissements, erreurs, confirmations sur stderr. `outil list | grep x` ne doit jamais être pollué.
3. **Jamais de surprise destructrice.** Toute écriture a un `--dry-run`, un plan affiché et une confirmation.
4. **Rien d'animé ni de coloré hors TTY.**
5. **Cœur séparé du rendu.** Les actions retournent des données ; seul le module de rendu écrit dans le terminal (ce qui permet un mode JSON et un futur serveur MCP).

## 2. Détection de l'environnement

| Condition | Comportement |
|---|---|
| stdout n'est pas un TTY | pas de couleur, pas de pager, pas de spinner, mode **flux** |
| `NO_COLOR` défini (non vide) ou `--no-color` | aucune couleur |
| `TERM=dumb` | aucune couleur, aucune animation |
| `CLICOLOR_FORCE=1` | couleurs forcées même hors TTY |
| locale non UTF-8 ou `--ascii` | icônes et bordures en ASCII |
| `CI=true` | comme hors TTY ; jamais de prompt interactif (échec explicite si une saisie est requise) |

La largeur du terminal est relue à chaque rendu (redimensionnement). Largeur inconnue : 80 colonnes.

## 3. Couleurs sémantiques

Les couleurs ont un **sens**, pas une décoration. Ne jamais coder une couleur en dur dans une commande : passer par un style nommé.

| Rôle | Couleur | Usage |
|---|---|---|
| `success` | vert | opération réussie, ✔ |
| `error` | rouge | échec, ✖ |
| `warning` | jaune | attention, état dégradé, ▲ |
| `info` | cyan | information neutre, ℹ |
| `accent` | magenta | éléments mis en avant (noms de repos, identifiants) |
| `muted` | gris | secondaire : dates, chemins, aides |
| `heading` | gras | en-têtes de colonnes, titres |

Règles : l'information ne doit **jamais** reposer sur la couleur seule (toujours une icône ou un mot) ; palette lisible sur fond clair **et** sombre (utiliser les couleurs ANSI adaptatives, pas de blanc/noir purs) ; dégrader proprement en 16 couleurs.

## 4. Iconographie

| Rôle | Unicode | ASCII |
|---|---|---|
| succès | ✔ | `[ok]` |
| erreur | ✖ | `[err]` |
| avertissement | ▲ | `[warn]` |
| info | ℹ | `[i]` |
| étape / flèche | → | `->` |
| puce | • | `*` |
| ellipse | … | `...` |

## 5. Listes et tableaux

- En-têtes en gras, séparés par un filet léger ; pas de bordure verticale chargée.
- Colonnes alignées ; **nombres alignés à droite**, texte à gauche.
- Valeurs humanisées : tailles (`12.4 MiB`), durées (`3m12s`), dates relatives en mode table (`il y a 3 j`) et ISO‑8601 en JSON.
- **Troncature** avec `…` quand la largeur est insuffisante ; la colonne « identifiante » (nom) est tronquée en dernier, les colonnes secondaires (dates, tailles) sont masquées en premier.
- Un compteur de fin : `42 résultats` (stderr), `0 résultat` explicite.
- Un mode `--wide` pour tout afficher, `--columns a,b,c` pour choisir.
- Les valeurs de correspondance (terme recherché) PEUVENT être surlignées.

Exemple :

```
  ARTIFACT                         VERSION          SIZE   MODIFIÉ
  com.acme:quality-core            1.4.2         3.1 MiB   il y a 2 j
  com.acme:quality-core            1.4.1         3.0 MiB   il y a 9 j
  com.acme:quality-web             2.0.0-SNAP…  12.4 MiB   il y a 1 h
42 résultats
```

## 6. Deux modes d'affichage des listes

### 6.1 Mode **flux** (`--stream`)

- **Défaut** quand stdout n'est pas un TTY, ou si `--stream`/`--no-pager`.
- Une ligne par résultat, écrite **dès qu'elle est connue** (pas d'attente de la fin de la pagination serveur).
- Pas d'en-têtes décoratives ni de largeur contrainte (colonnes séparées par un espace ou une tabulation selon `--output`).
- Interrompu proprement par `SIGPIPE` / `Ctrl-C` (aucune trace d'erreur sur `| head`).

### 6.2 Mode **pagination** (pager interactif)

- **Défaut** sur un TTY quand le nombre de lignes dépasse la hauteur de l'écran.
- Désactivable : `--no-pager`, `--stream`, ou `PAGER=` vide. Respecter `$PAGER` si l'utilisateur en a configuré un (hors mode intégré).
- Pager intégré :

| Touche | Action |
|---|---|
| `↓` `j` / `↑` `k` | ligne suivante / précédente |
| `Espace` `PgDn` / `b` `PgUp` | page suivante / précédente |
| `g` / `G` | début / fin |
| `/` puis `n` `N` | recherche et occurrences |
| `q` `Esc` | quitter |
| `?` | aide |

- Barre d'état en bas : `1–38 / 1 234   (↑↓ défiler · / chercher · q quitter)`.
- En-têtes de colonnes **épinglés** en haut.
- **Chargement paresseux** : quand la source est paginée côté serveur, la page suivante est chargée à l'approche de la fin (`chargement…` dans la barre d'état), sans bloquer le défilement.
- Quitter le pager ne réaffiche pas le contenu ; `--keep` PEUT le laisser dans le terminal.

### 6.3 Choix automatique

```
--output json|ndjson|plain  → flux, sans décor
stdout non-TTY              → flux
TTY et résultat court       → tableau direct
TTY et résultat long        → pager
```

## 7. Sorties machine

- `--output table|plain|json|ndjson` (défaut `table` sur TTY, `plain` sinon).
- `json` : un objet ou tableau **stable, documenté et versionné** (`"schema": 1`), clés en `snake_case`, dates ISO‑8601 UTC, tailles en octets.
- `ndjson` : un objet par ligne, compatible flux.
- `plain` : colonnes séparées par tabulation, sans en-tête (`--header` pour l'ajouter).
- Les erreurs en mode JSON sont émises sur stderr au format `{"error":{"code":"…","message":"…","hint":"…"}}`.

## 8. Interactions

- **Saisie secrète** (mots de passe, jetons) : masquée, jamais écho, jamais en argument de ligne de commande (visible via `ps`). Variables d'environnement acceptées pour l'automatisation.
- **Confirmation** pour toute action qui modifie ou supprime : récapitulatif des effets, puis `Continuer ? [o/N]` (défaut = non). Pour une suppression de masse, demander de **retaper un nom** ou un nombre.
- `--yes` / `-y` saute la confirmation ; sans TTY et sans `--yes`, l'action destructrice **échoue** au lieu de bloquer.
- `--dry-run` : montre exactement ce qui serait fait, ne modifie rien, code retour 0.
- Ctrl‑C : arrêt propre, état partiel indiqué, code 130.

## 9. Progression

- **Durée inconnue** : spinner + libellé d'étape (`⠋ Recherche des assets…`).
- **Taille connue** : barre `[██████░░░░] 61 %  7.6/12.4 MiB  3.2 MiB/s  ETA 2 s`.
- Plusieurs étapes : liste d'étapes, chacune terminée par ✔/✖ et sa durée.
- Hors TTY : une ligne de log par étape, sans animation.

## 10. Messages et erreurs

Format **quoi / pourquoi / que faire** :

```
✖ Impossible de promouvoir com.acme:quality-core:1.4.2
  La version existe déjà dans rdsf-qp-maven-release (politique de réécriture : interdite).
  → Utilisez --force si le dépôt autorise la réécriture, ou choisissez une autre version.
```

- Une erreur = une raison principale ; les détails techniques derrière `--verbose` / `-v` (`-vv` pour les requêtes HTTP, **jamais les secrets**).
- Les messages sont courts, à l'indicatif, sans majuscules criardes ni points d'exclamation.
- Suggestions de correction (« vouliez-vous dire … ? ») sur commande ou alias inconnu.

### Codes retour

| Code | Signification |
|---|---|
| 0 | succès (y compris `--dry-run`) |
| 1 | erreur générale |
| 2 | mauvais usage (arguments/flags invalides) |
| 3 | échec **partiel** (une partie des éléments a réussi) |
| 4 | authentification / autorisation refusée |
| 130 | interrompu par l'utilisateur |

## 11. Aide et découvrabilité

- Chaque commande a : un résumé d'une ligne, une description, des **exemples** concrets, la liste des flags groupés par thème.
- `--help` est coloré (commandes en accent, flags en cyan) ; `-h` est un alias.
- Complétion shell (bash, zsh, fish, powershell) fournie, y compris des **valeurs dynamiques** (noms de dépôts configurés).
- `--version` affiche version, commit et date de build.

## 12. Conventions de commande

- Forme `outil <verbe> [cibles…] [flags]` ; verbes en anglais, courts, cohérents entre outils (`init`, `list`, `show`, `search`, `remove`, `clean`, `promote`).
- Flags longs en `--kebab-case`, abréviations courtes seulement pour les très fréquents (`-o`, `-y`, `-v`, `-n` pour dry-run si usage établi).
- Flags globaux communs à tous les outils : `--output/-o`, `--no-color`, `--ascii`, `--no-pager`, `--stream`, `--yes/-y`, `--dry-run`, `--verbose/-v`, `--help/-h`, `--version`.
- Idempotence : relancer une commande interrompue ne doit pas corrompre l'état ; fournir `--resume` quand une opération est longue.
- Configuration par utilisateur dans `~/.<outil>/` ; **jamais de secret dans un fichier de configuration en clair** (trousseau du système, ou fichier `0600` en dernier recours avec avertissement).

## 13. Tests de l'UX

- Tests « golden » : le rendu d'un jeu de données fixe est comparé à un fichier de référence, dans chaque mode (couleur / sans couleur / ASCII / largeurs 60-80-120).
- Les tests forcent un environnement sans TTY pour les modes flux et JSON.
- Test de non‑régression : aucune séquence ANSI dans une sortie non‑TTY.

## 14. Implémentation de référence (Go) — informatif

| Besoin | Bibliothèque |
|---|---|
| commandes, flags, complétion | `spf13/cobra` |
| styles, couleurs adaptatives | `charmbracelet/lipgloss` |
| pager, spinner, barre, prompts | `charmbracelet/bubbletea` + `bubbles` |
| détection TTY / taille | `golang.org/x/term` |
| trousseau | `zalando/go-keyring` |

D'autres langages sont libres de suivre la charte avec leurs équivalents (Rich/Typer en Python, Ink/Chalk en Node, Picocli/JLine en Java) tant que les sections 2 à 12 sont respectées.
