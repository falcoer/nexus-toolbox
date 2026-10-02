# Promouvoir un artifact snapshot vers la release — aide-mémoire

Commande courte, dans l'ordre : **l'artifact, puis la version cible.**

```powershell
.\nexus.exe promote flux-editor 18.00.00.beta1-0 --dry-run     # 1. voir le plan, rien n'est écrit
.\nexus.exe promote flux-editor 18.00.00.beta1-0               # 2. promouvoir (confirmation demandée)
.\nexus.exe info rel com.arcelormittal.fluxmanager:flux-editor:18.00.00.beta1-0 --all   # 3. vérifier
```

Un doute sur la syntaxe ? Lancez `nexus promote` **sans argument** : l'assistant pose les questions
(artifact, version, build, version cible), montre le plan, demande confirmation, puis affiche la
commande courte à réutiliser.

## Les 4 règles appliquées automatiquement

1. **Dépôts** : la paire snapshot → release est déduite des repos configurés (politique de version
   SNAPSHOT / RELEASE). Sinon : `--from` / `--to`, ou `nexus repos link <snapshot> <release>` une fois pour toutes.
2. **Artifact** : l'artifactId suffit (`flux-editor`) ; le groupe est retrouvé par recherche. S'il y en a
   plusieurs, l'outil vous demande lequel (ou liste les `groupId:artifactId` possibles).
3. **Source** : la version snapshot la plus récente et son build le plus récent. Pour en choisir une autre :
   `--from-version 18.00.00-0-SNAPSHOT`, `--build 3`.
4. **Cible** : le second argument ; sans lui, la version source sans `-SNAPSHOT`.

## Les modules requis : « en release », « à promouvoir », « bloquant »

Un artifact dépend d'autres modules : son parent, ses dépendances, les BOM importés, les librairies
dont la version est portée par une propriété (`fox.version`…). L'outil les suit tous (et leurs propres
parents) et décide pour chacun :

| statut | sens | conséquence |
|---|---|---|
| **en release** | la version voulue existe déjà dans le repo release | rien à publier, la référence est figée dessus |
| **à promouvoir** | un build snapshot existe | il sera publié **avant** l'artifact |
| **bloquant** | introuvable partout | **rien n'est publié** : une release incohérente est pire qu'une release absente |

Version donnée à chaque module : celle de l'artifact si le module partage sa version de base (même
reactor : `_flux-manager-parent` suit `flux-editor`), sinon sa propre version sans `-SNAPSHOT`.

### Quand il y a des modules à promouvoir

Le plan est affiché, puis :
- **dans un terminal** : « Ce plan publie N artifacts… L'exécuter ? » (un `--yes` seul ne répond pas à cette question) ;
- **sans terminal** (script) : la commande s'arrête avec le **code retour 5** ; relire le plan puis relancer avec `--with-deps`.

### Quand un module est bloquant

Le plan dit ce qui existe (versions en release, versions snapshot) et quoi faire :
- promouvoir d'abord ce module (il lui faut un build snapshot) ;
- ou viser une version qui existe : `--pin groupId:artifactId=<version>` (parent, dépendance) ou
  `--set-property nom=<version>` (propriété du pom).

## Après la promotion

- Chaque fichier est publié avec ses `.sha1` et `.md5`, et vérifié ; un fichier `…-promoted-from-<version d'origine>.txt` trace la provenance.
- Relancer la même commande est sans risque : les fichiers identiques sont ignorés, les empreintes manquantes sont ajoutées.
- Remplacer une version déjà publiée (nouveau build) : ajouter `--force` (si le repo release autorise le redeploy ; sinon supprimer la version dans l'interface Nexus).
- `maven-metadata.xml` est généré par Nexus ; si l'outil avertit qu'il ne liste pas la version, lancer la tâche « Rebuild Maven repository metadata ».

## Codes retour

0 ok · 1 erreur · 2 usage · 3 partiel · 4 accès refusé · **5 plan à valider (`--with-deps`)** · 130 interrompu.

## Options avancées (rarement utiles)

`--pin`, `--set-property`, `--align-properties`, `--allow-snapshot-refs`, `--no-marker`, `--delete-source`,
et l'ancienne forme complète `nexus promote <src> <dst> <groupId:artifactId:version> [--as-version X]`.

## Limites

- Les binaires (`.zip`, `.jar`, `.war`) sont copiés tels quels : leur contenu interne (noms de dossiers, métadonnées) garde la version SNAPSHOT du build. Pour qu'il porte la version de la release, il faut le corriger à la source (assembly) et rebuilder.
- Seules les références écrites dans les poms de la chaîne sont suivies (pas les dépendances transitives).
