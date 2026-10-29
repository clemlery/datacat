# SPEC : datacat

API REST en Go qui gère un catalogue de jeux de données (*datasets*) : CRUD, recherche par tags, soft delete avec restauration. Persistance PostgreSQL 18, exécution sous Podman rootless avec `podman compose`.

---

## 1. Périmètre

### Inclus (v1)
- CRUD sur les datasets : création, lecture, remplacement `PUT`, suppression logique.
- Restauration d'un dataset supprimé.
- Ajout / retrait unitaire de tags sur un dataset.
- Listing filtrable par tags (au moins un des tags demandés), sans pagination.
- Migrations SQL versionnées, appliquées au démarrage de l'API.
- Healthchecks, logs structurés, arrêt gracieux.
- Tests unitaires (service) et tests de handlers (`httptest`).
- Vérification manuelle des règles SQL via la fixture (§11).
- Outillage : Makefile, golangci-lint (configuration par défaut), fixture de données.

### Exclu de la v1
- Tout ce qui figure dans la section 12 (exercice de validation).
- `PATCH` (mise à jour partielle) : seule la mise à jour par `PUT` existe.
- Recherche texte (`q`), extension `pg_trgm`, recherche full-text.
- Paramètre de tri : l'ordre du listing est fixe (§5.8).
- Authentification / autorisation (API ouverte).
- Métriques Prometheus.
- Endpoint de listing des tags (`/tags`).
- Contrôle de concurrence (ETag / If-Match) : la dernière écriture gagne.
- Purge définitive des datasets supprimés. Conséquence assumée : un nom utilisé une fois n'est plus jamais réutilisable.
- Documentation OpenAPI.
- CI GitHub Actions.
- Image distroless et sous-commande `healthcheck`.

---

## 2. Stack technique

| Élément | Choix |
|---|---|
| Langage | Go 1.26 (module `github.com/clemlery/datacat`) |
| Routeur HTTP | `net/http` de la bibliothèque standard (`http.ServeMux` avec méthodes et path params) |
| Accès BD | `github.com/jackc/pgx/v5` + `pgxpool`, SQL écrit à la main |
| Migrations | `github.com/golang-migrate/migrate/v4` (source `iofs` sur fichiers embarqués, driver `pgx/v5`) |
| Normalisation Unicode | `golang.org/x/text/unicode/norm` (NFC) |
| Logs | `log/slog`, sortie JSON |
| Base de données | PostgreSQL 18.6 |
| Conteneurs | Podman rootless, `podman compose` (fournisseur : docker-compose) |
| Lint | golangci-lint, linters par défaut, pas de fichier de configuration |

Les dépendances outils (CLI `migrate`) sont déclarées via la directive `tool` de `go.mod` et invoquées par `go tool`.

---

## 3. Architecture

Architecture en couches, dépendances orientées vers l'intérieur, découplage par interfaces :

```
handlers (HTTP)  ──►  service (règles métier)  ──►  repository (SQL)
        └──────────────► domain (modèles, erreurs) ◄──────────┘
```

- **domain** : types `Dataset`, `ListQuery`, valeurs autorisées (formats), erreurs sentinelles (`ErrNotFound`, `ErrConflict`, `ErrNotDeleted`, `ValidationError` portant une liste d'erreurs par champ). Aucune dépendance externe.
- **repository** : interface `DatasetRepository` définie côté service ; implémentation PostgreSQL (pgx). Traduit les erreurs PostgreSQL (`23505` unique_violation → `ErrConflict`, aucune ligne → `ErrNotFound`). Les opérations multi-tables (dataset + tags) s'exécutent dans une transaction.
- **service** : validation, normalisation (§4.5), orchestration. Interface `DatasetService` consommée par les handlers.
- **handlers** : décodage/encodage JSON, lecture des paramètres, mapping erreurs domaine → Problem Details. Aucune logique métier.
- **main** : chargement de la config, migrations, création du pool, câblage des dépendances, démarrage du serveur, arrêt gracieux.

### Arborescence cible

```
cmd/datacat/main.go            # point d'entrée
internal/config/               # lecture des variables d'environnement
internal/domain/               # modèles, erreurs, constantes
internal/service/              # logique métier + tests unitaires
internal/repository/postgres/  # implémentation pgx
internal/httpapi/              # handlers, routes, middlewares, problem details + tests httptest
migrations/                    # *.up.sql / *.down.sql + migrations.go (embed.FS)
scripts/seed.sql               # fixture de vérification (§11), dev uniquement
Containerfile
compose.yaml
Makefile
```

---

## 4. Modèle de données

Toutes les longueurs de cette spec sont exprimées en **caractères Unicode (runes)**, mesurées en Go avec `utf8.RuneCountInString` et jamais avec `len()`. Exemple : `santé` fait 5 runes (6 octets).

### 4.1 Table `datasets`

| Colonne | Type | Contraintes |
|---|---|---|
| `id` | `uuid` | PK, `DEFAULT uuidv7()` (natif PG 18) |
| `name` | `text` | `NOT NULL`, 1 à 200 runes après normalisation, **unique** (y compris parmi les supprimés) |
| `description` | `text` | nullable, ≤ 5 000 runes |
| `source_url` | `text` | nullable, URL absolue (`http`, `https`, `s3`, `gs`, `ftp`, `file`) |
| `format` | `text` | `NOT NULL`, `CHECK` dans la liste fermée (§4.4) |
| `size_bytes` | `bigint` | nullable, `CHECK (size_bytes >= 0)` |
| `row_count` | `bigint` | nullable, `CHECK (row_count >= 0)` |
| `owner` | `text` | nullable, ≤ 200 runes |
| `license` | `text` | nullable, ≤ 100 runes (ex. identifiant SPDX, non validé) |
| `created_at` | `timestamptz` | `NOT NULL DEFAULT now()` |
| `updated_at` | `timestamptz` | `NOT NULL DEFAULT now()`, règle de mise à jour en §5.2 |
| `deleted_at` | `timestamptz` | nullable ; non nul = dataset supprimé (soft delete) |

Index :
- unique sur `lower(name)`, **non partiel** : unicité insensible à la casse, et le nom d'un dataset supprimé reste réservé à vie (pas de purge, §1).
- index sur `created_at` (ordre du listing).

### 4.2 Table `tags`

| Colonne | Type | Contraintes |
|---|---|---|
| `id` | `bigint` | PK, `GENERATED ALWAYS AS IDENTITY` |
| `name` | `text` | `NOT NULL UNIQUE`, `CHECK (char_length(name) BETWEEN 1 AND 50)` |

La normalisation des tags est faite **uniquement en Go** (§4.5) : aucune contrainte SQL ne recalcule `lower()` ou `btrim()`, pour éviter tout écart entre Go et PostgreSQL. L'unicité porte sur la valeur déjà normalisée.

Les tags orphelins (plus rattachés à aucun dataset) sont conservés ; ils sont sans effet fonctionnel.

### 4.3 Table `dataset_tags`

| Colonne | Type | Contraintes |
|---|---|---|
| `dataset_id` | `uuid` | FK → `datasets(id)` `ON DELETE CASCADE` |
| `tag_id` | `bigint` | FK → `tags(id)` `ON DELETE RESTRICT` |

PK `(dataset_id, tag_id)`, index sur `tag_id`.

### 4.4 Formats autorisés

`csv`, `json`, `parquet`, `xlsx`, `xml`, `geojson`, `other`.

### 4.5 Normalisation

**Nom d'un dataset** : normalisation Unicode NFC, puis `strings.TrimSpace`. La casse est conservée (l'unicité insensible à la casse est assurée par l'index `lower(name)`).

**Tags**, dans cet ordre :
1. normalisation Unicode NFC (`santé` composé et décomposé donnent le même tag) ;
2. `strings.TrimSpace` ;
3. `strings.ToLower`. Les accents sont conservés (`Santé` → `santé`).

Après normalisation :
- 1 à 50 runes ; lettres Unicode, chiffres, `-`, `_` et espaces internes simples ;
- doublons dans une même requête : dédupliqués silencieusement ;
- maximum 20 tags par dataset ;
- les tags sont créés à la volée (`INSERT … ON CONFLICT DO NOTHING`) ;
- les tags d'un dataset sont toujours renvoyés triés par ordre d'octets (`ORDER BY name COLLATE "C"`).

---

## 5. API HTTP

Préfixe : `/api/v1`. Corps de requête et de réponse en JSON (`application/json`), erreurs en `application/problem+json`.

### 5.1 Représentation d'un dataset

```json
{
  "id": "01933b8e-6f7a-7c3e-9a51-3f2d1b0c4e5a",
  "name": "Population communale 2024",
  "description": "Population légale par commune.",
  "source_url": "https://example.org/pop-2024.csv",
  "format": "csv",
  "size_bytes": 4823112,
  "row_count": 34935,
  "owner": "Équipe Démographie",
  "license": "etalab-2.0",
  "tags": ["2024", "démographie", "france"],
  "created_at": "2026-10-29T09:12:00Z",
  "updated_at": "2026-10-29T09:12:00Z",
  "deleted_at": null
}
```

Tous les champs sont toujours présents, dans toutes les réponses (lecture et listing) ; les valeurs absentes valent `null`, `tags` vaut `[]` si vide. Aucun champ supplémentaire n'est ajouté selon les paramètres. Dates en RFC 3339 UTC.

### 5.2 Endpoints

| Méthode | Chemin | Description | Succès |
|---|---|---|---|
| `POST` | `/api/v1/datasets` | Créer un dataset | `201` + header `Location` + corps |
| `GET` | `/api/v1/datasets` | Lister / filtrer par tags | `200` |
| `GET` | `/api/v1/datasets/{id}` | Lire un dataset | `200` |
| `PUT` | `/api/v1/datasets/{id}` | Remplacer un dataset | `200` |
| `DELETE` | `/api/v1/datasets/{id}` | Soft delete | `204` |
| `POST` | `/api/v1/datasets/{id}/restore` | Restaurer un dataset supprimé | `200` + corps |
| `POST` | `/api/v1/datasets/{id}/tags/{tag}` | Ajouter un tag (idempotent) | `204` |
| `DELETE` | `/api/v1/datasets/{id}/tags/{tag}` | Retirer un tag (idempotent) | `204` |
| `GET` | `/healthz` | Liveness | `200` |
| `GET` | `/readyz` | Readiness (ping BD) | `200` / `503` |

Règles communes :
- `{id}` non UUID → `400`.
- Un dataset supprimé est traité comme inexistant (`404`) par toutes les opérations sauf `GET /datasets/{id}?include_deleted=true`, `GET /datasets?include_deleted=true` et `POST …/restore`.
- **Règle `updated_at`** : il est mis à jour à `now()` à chaque écriture réussie (`PUT`, suppression, restauration, ajout ou retrait de tag), **même si l'écriture ne change aucune valeur**. Une seule règle, pas de comparaison avant/après.

### 5.3 Création : `POST /datasets`

Corps : `name` et `format` obligatoires ; `description`, `source_url`, `size_bytes`, `row_count`, `owner`, `license`, `tags` optionnels. `id`, `created_at`, `updated_at`, `deleted_at` ne sont pas acceptés (champ inconnu → `400`).

Erreurs : `422` validation, `409` nom déjà utilisé (casse différente comprise, dataset supprimé compris).

### 5.4 Remplacement : `PUT /datasets/{id}`

Même schéma que la création. Remplace l'intégralité de la ressource : tout champ optionnel absent devient `null`, `tags` absent devient une liste vide.

Erreurs : `404` inexistant ou supprimé, `422` validation, `409` si le nouveau nom est déjà utilisé par un autre dataset (casse différente comprise, dataset supprimé compris). Renommer un dataset avec son propre nom, même avec une casse différente, n'est pas un conflit.

### 5.5 Suppression : `DELETE /datasets/{id}`

Positionne `deleted_at = now()`. Les liaisons de tags sont conservées (pour la restauration). Dataset déjà supprimé ou inexistant → `404`.

### 5.6 Restauration : `POST /datasets/{id}/restore`

Remet `deleted_at` à `null` et renvoie le dataset avec ses tags. Inexistant → `404`, non supprimé → `409`. Aucun conflit de nom possible (le nom reste réservé à vie).

### 5.7 Tags unitaires : `POST|DELETE /datasets/{id}/tags/{tag}`

`{tag}` est décodé (URL-encoding) puis normalisé selon §4.5. Ajout d'un tag déjà présent ou retrait d'un tag absent → `204` sans modification des liaisons (`updated_at` mis à jour quand même, §5.2). Tag invalide ou dépassement de 20 tags → `422`.

### 5.8 Listing : `GET /datasets`

Paramètres de requête :

| Paramètre | Type | Défaut | Description |
|---|---|---|---|
| `tag` | string, répétable | aucun | Tags recherchés, normalisés selon §4.5, maximum 20 |
| `include_deleted` | bool (`true`/`false`) | `false` | Inclut les datasets supprimés |

Règles :
- sans `tag` : tous les datasets (non supprimés, sauf `include_deleted=true`) ;
- avec un ou plusieurs `tag` : seuls les datasets possédant **au moins un** des tags demandés sont retournés (OU) ; chaque dataset n'apparaît qu'une fois ;
- ordre fixe : `created_at` décroissant, puis `id` décroissant comme départage ;
- pas de pagination : tous les résultats sont renvoyés.

Paramètre invalide (plus de 20 tags, tag invalide après normalisation, `include_deleted` autre que `true`/`false`, paramètre inconnu) → **`400`**. Le `422` est réservé à la validation des corps de requête.

Réponse :

```json
{
  "items": [ { "...dataset...": "..." } ]
}
```

### 5.9 Erreurs : RFC 9457 Problem Details

```json
{
  "type": "https://datacat.local/problems/validation-error",
  "title": "Validation failed",
  "status": 422,
  "detail": "Le corps de la requête contient des champs invalides.",
  "instance": "/api/v1/datasets",
  "request_id": "b3c1…",
  "errors": [
    { "field": "format", "message": "doit valoir l'une des valeurs : csv, json, parquet, xlsx, xml, geojson, other" }
  ]
}
```

| Statut | `type` (suffixe) | Cas |
|---|---|---|
| `400` | `bad-request` | JSON mal formé, champ inconnu, paramètre de requête invalide (§5.8), id non UUID |
| `404` | `not-found` | Dataset inexistant ou supprimé, route inconnue |
| `405` | `method-not-allowed` | Méthode non supportée (header `Allow`) |
| `409` | `conflict` | Nom déjà pris (`POST` et `PUT`), restauration d'un dataset non supprimé |
| `413` | `payload-too-large` | Corps > 1 Mio |
| `415` | `unsupported-media-type` | `Content-Type` autre que `application/json` |
| `422` | `validation-error` | Règles de validation des corps et des tags unitaires (avec `errors[]`) |
| `500` | `internal-error` | Erreur inattendue (détail jamais exposé, loggé côté serveur) |
| `503` | `unavailable` | `/readyz` quand la BD ne répond pas |

Le décodage JSON rejette les champs inconnus (`DisallowUnknownFields`) et les corps contenant plusieurs valeurs JSON.

---

## 6. Configuration

Variables d'environnement :

| Variable | Défaut | Description |
|---|---|---|
| `DATABASE_URL` | aucun (obligatoire) | DSN PostgreSQL |
| `HTTP_ADDR` | `:8080` | Adresse d'écoute |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DB_MAX_CONNS` | `10` | Taille max du pool pgx |
| `SHUTDOWN_TIMEOUT` | `10s` | Délai max de l'arrêt gracieux |

Config invalide ou manquante → l'application s'arrête au démarrage avec un message explicite.

---

## 7. Exploitation

### 7.1 Démarrage
1. Lecture de la configuration.
2. Connexion à la BD avec retries (backoff exponentiel, environ 30 s max).
3. Application des migrations embarquées (golang-migrate ; verrou consultatif natif). Échec → arrêt avec code non nul. Base en état `dirty` → arrêt avec message explicite.
4. Démarrage du serveur HTTP avec timeouts (`ReadHeaderTimeout` 5 s, `ReadTimeout` 15 s, `WriteTimeout` 30 s, `IdleTimeout` 60 s).

### 7.2 Migrations
- Fichiers `migrations/NNNNNN_description.up.sql` / `.down.sql`, embarqués via `embed.FS`.
- Première migration : tables, contraintes, index.
- Création d'une migration : `make migrate-new name=…` (CLI `migrate` via `go tool`).

### 7.3 Healthchecks
- `/healthz` : `200 {"status":"ok"}` dès que le processus sert des requêtes.
- `/readyz` : ping BD avec timeout de 2 s ; `200` ou `503`.

### 7.4 Logs
- `slog` en JSON sur stdout.
- Middleware de request ID : réutilise `X-Request-ID` entrant s'il est présent et valide, sinon en génère un ; renvoyé dans la réponse et inclus dans chaque log et dans les Problem Details.
- Log d'accès par requête : méthode, chemin, statut, durée, taille de réponse, request ID.
- Middleware de recover : une panique produit un `500` Problem Details et un log `error` avec stack.

### 7.5 Arrêt gracieux
`signal.NotifyContext` sur `SIGINT`/`SIGTERM` → `/readyz` passe en `503` → `http.Server.Shutdown` avec `SHUTDOWN_TIMEOUT` → fermeture du pool pgx → sortie code 0.

---

## 8. Conteneurisation (Podman rootless)

### 8.1 Image de l'API : `Containerfile`
- Multi-stage : build sur `docker.io/library/golang:1.26`, `CGO_ENABLED=0`, binaire statique, `-trimpath -ldflags="-s -w"`.
- Image finale : `docker.io/library/alpine:3.22`, exécution sous un utilisateur non root créé dans l'image. `wget` (busybox) sert au healthcheck.
- **Pas d'instruction `HEALTHCHECK` dans le `Containerfile`** : `podman build` produit une image au format OCI, qui ignore cette instruction. Le healthcheck est défini dans `compose.yaml`.
- Noms d'images **pleinement qualifiés** (registre explicite) : Podman refuse ou demande confirmation pour les noms courts.

### 8.2 `compose.yaml`
Deux services :

**`db`**
- Image `docker.io/library/postgres:18.6`.
- Variables `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` lues depuis un fichier `.env` (non versionné ; `.env.example` versionné).
- Volume nommé monté sur **`/var/lib/postgresql`** (et non `/var/lib/postgresql/data`) : depuis PostgreSQL 18, `PGDATA` vaut `/var/lib/postgresql/18/docker` et le `VOLUME` de l'image est `/var/lib/postgresql`. Ne pas surcharger `PGDATA`.
- Healthcheck `pg_isready -U $POSTGRES_USER -d $POSTGRES_DB`.
- Port publié uniquement sur `127.0.0.1:5432` (usage dev / `make seed`).

**`api`**
- Construite depuis le `Containerfile`.
- `DATABASE_URL` pointant sur le service `db`.
- `depends_on` `db` avec `condition: service_healthy`.
- Healthcheck : `wget -q -O /dev/null http://127.0.0.1:8080/healthz`.
- Port publié `127.0.0.1:8080:8080`.

Contraintes rootless : uniquement des ports > 1024 ; volumes nommés plutôt que bind mounts (si bind mount, suffixe `:Z` pour SELinux sur Fedora).

---

## 9. Tests

- **Service (unitaires)** : repository simulé par un fake en mémoire implémentant l'interface ; le fake reproduit l'unicité insensible à la casse (y compris parmi les supprimés) en renvoyant `ErrConflict`. Couvre validation, normalisation (NFC, trim, minuscules, runes), limites (20 tags, longueurs), sémantique `PUT` (champs absents → `null`), `409` sur création et renommage, règle `updated_at`, propagation des erreurs domaine.
- **Handlers (`httptest`)** : service simulé. Couvre routage, codes de statut, décodage strict (champs inconnus, JSON invalide, taille max, `Content-Type`), parsing et validation des paramètres de listing (`400`), format Problem Details, header `Location`, request ID.
- Tests table-driven, `go test -race ./...`, sans dépendance à une base réelle.
- Les règles portées par le SQL (filtre OU par tags, ordre, unicité incluant les supprimés, soft delete) sont couvertes en v1 par les cas manuels de la section 11, exécutés contre la stack compose. Leur automatisation fait partie de l'exercice de validation (§12).

---

## 10. Outillage

### Makefile
| Cible | Action |
|---|---|
| `up` / `down` | `podman compose up -d --build` / `podman compose down` |
| `logs` | Logs des services |
| `run` | Lance l'API en local (`go run ./cmd/datacat`) contre la BD compose |
| `build` | Compile le binaire |
| `test` | `go test -race ./...` |
| `lint` | `golangci-lint run` (linters par défaut) |
| `seed` | Charge `scripts/seed.sql` via `podman compose exec db psql …` |
| `reset` | Repart d'une base vide : `podman compose down -v` (supprime le volume nommé), puis `podman compose up -d --build --wait` (attend que `db` et `api` soient `healthy`, donc migrations appliquées), puis `make seed`. **Destructif** : efface toutes les données. |
| `migrate-new` | Crée une paire de fichiers de migration |
| `psql` | Shell psql dans le conteneur `db` |

---

## 11. Vérification

### 11.1 Fixture (`scripts/seed.sql`)

Toujours chargée sur une base vide via `make reset` (jamais `make seed` seul, qui l'ajouterait à des données existantes). Les `created_at` sont fixés explicitement pour rendre l'ordre déterministe. Les `id` de D6 et D8 sont aussi fixés explicitement, car ils partagent le même `created_at` et c'est le départage par `id` qui décide de leur ordre ; les autres `id` sont générés par `uuidv7()`. Jamais appliquée automatiquement ni incluse dans les migrations.

| Réf. | `name` | `format` | `tags` | `created_at` | Supprimé |
|---|---|---|---|---|---|
| D1 | Population communale 2024 | csv | `2024`, `démographie`, `france` | 2026-01-01T00:00:00Z | non |
| D2 | Qualité de l'air Paris | json | `environnement`, `france` | 2026-01-02T00:00:00Z | non |
| D3 | Températures mondiales | parquet | `climat`, `environnement` | 2026-01-03T00:00:00Z | non |
| D4 | Naissances par région | csv | `démographie`, `santé` | 2026-01-04T00:00:00Z | non |
| D5 | Hôpitaux de France | geojson | `france`, `santé` | 2026-01-05T00:00:00Z | non |
| D6 | Ventes fictives | xlsx | aucun | 2026-01-06T00:00:00Z | non |
| D7 | Ancien recensement 1999 | csv | `démographie`, `france` | 2026-01-07T00:00:00Z | oui |
| D8 | Stocks fictifs | xlsx | aucun | 2026-01-06T00:00:00Z | non |

`id` fixés : D6 = `01900000-0000-7000-8000-000000000006`, D8 = `01900000-0000-7000-8000-000000000008`. D8 a donc un `id` supérieur à D6.

### 11.2 Cas attendus

Chaque cas part d'une fixture fraîche (`make reset`), sauf V9 et V10 qui s'enchaînent : V10 s'exécute juste après V9, sans `reset` entre les deux. Les listes sont données dans l'ordre exact attendu.

| # | Requête | Résultat attendu |
|---|---|---|
| V1 | `GET /api/v1/datasets` | `200`, D8, D6, D5, D4, D3, D2, D1 (D7 absent) |
| V1b | Départage par `id` : position relative de D8 et D6 dans V1 | D8 juste avant D6 (même `created_at`, `id` de D8 supérieur, ordre `id` décroissant) |
| V2 | `GET /api/v1/datasets?tag=santé` | `200`, D5, D4 |
| V3 | `GET /api/v1/datasets?tag=France&tag=climat` | `200`, D5, D3, D2, D1 (OU ; `France` normalisé en `france` ; D7 absent car supprimé) |
| V4 | `GET /api/v1/datasets?tag=sante%CC%81` (forme décomposée) | `200`, D5, D4 (identique à V2 grâce à NFC) |
| V5 | `GET /api/v1/datasets?tag=démographie&include_deleted=true` | `200`, D7, D4, D1 ; D7 a `deleted_at` renseigné |
| V6 | `POST` avec `name` = `population communale 2024` | `409` (casse différente) |
| V7 | `POST` avec `name` = `Ancien recensement 1999` | `409` (nom d'un dataset supprimé, réservé à vie) |
| V8 | `PUT` de D6 avec `name` = `Hôpitaux de France` | `409` |
| V9 | `DELETE` D2, puis `GET` D2 | `204`, puis `404` |
| V10 | Suite de V9 : `GET` D2 `?include_deleted=true`, puis `POST …/restore`, puis `GET` D2 | `200` avec `deleted_at` renseigné ; `200` ; `200` avec `deleted_at` à `null` et `tags` = `["environnement", "france"]` |

### 11.3 Critères d'acceptation

1. `make up` sur une machine avec Podman rootless démarre `db` et `api` ; les deux services passent `healthy` ; `/readyz` répond `200`.
2. Un redémarrage simple (`make down && make up`, sans `-v` et donc **sans** `make reset`) conserve les données ; le volume est bien monté sur `/var/lib/postgresql`.
3. Les migrations s'appliquent au premier démarrage et sont sans effet aux suivants.
4. Les cas V1 à V10 (V1b compris) donnent exactement les résultats attendus.
5. Toutes les erreurs sont au format `application/problem+json` avec `request_id`.
6. `SIGTERM` sur l'API : les requêtes en cours se terminent, le processus sort en code 0.
7. `make lint` et `make test` passent.

---

## 12. Exercice de validation (hors v1)

À réaliser après la v1, dans cet ordre :
- Pagination du listing (`limit`, `offset`) avec `total` indépendant de la pagination.
- Classement par pertinence : tri par nombre de tags demandés possédés (décroissant), puis ordre par défaut, puis `id` ; champ `matched_tags` dans chaque élément.
- Filtres combinés (tags et autres critères en ET).
- Tests d'intégration contre un PostgreSQL réel, automatisant au minimum les cas V1 à V10.