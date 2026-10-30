# PLAN : datacat v1

Plan d'implémentation de la v1 décrite dans [`SPEC.md`](../SPEC.md), découpé en **briques logiques de taille comparable**, implémentées une par une en TDD.

---

## 0. État de départ : le squelette (fait)

| Commit | Contenu |
|---|---|
| `9b6160c` | Schéma initial (`000001_init`) et migrations embarquées |
| `d0aebac` | Lecture de la configuration (`internal/config`) |
| `fefc36d` | Démarrage de l'API : pool + ping avec retries, migrations, `/healthz`, `/readyz`, arrêt gracieux |
| `ad93e62` | `Containerfile`, `compose.yaml`, `Makefile` |

Déjà en place et **à ne pas refaire** : `cmd/datacat/main.go`, `internal/config`, `internal/repository/postgres/{pool,migrate}.go`, `internal/httpapi/{health,problem,router}.go` (writer Problem Details minimal, sans `request_id`), `migrations/000001_init.*.sql`, outillage Podman et Makefile.

Reste à faire : domaine, service, handlers `/api/v1`, middlewares, repository SQL, fixture, recette.

---

## 1. Méthode : TDD en circuit court

Chaque brique suit le même cycle, et **une brique est terminée avant que la suivante commence**.

1. **Rouge** : écrire les tests de la brique (table-driven), avec juste assez de code (types, signatures, `panic("TODO")` ou valeurs zéro) pour que **ça compile**. Lancer `make test` : les nouveaux tests échouent, et ils échouent **pour la bonne raison** (assertion, pas erreur de compilation).
2. **⏸ Arrêt n°1 (D11)** : relecture des cas de test par l'utilisateur avant toute implémentation.
3. **Vert** : implémenter le minimum pour que **tous** les tests passent, ceux de la brique et ceux des briques précédentes.
4. **Refactor** : nettoyer (noms, duplication, découpage) sans changer le comportement ; les tests restent verts. `make lint` passe.
5. **⏸ Arrêt n°2 (D11)** : rapport de fin de brique (format dans `CLAUDE.md`). L'utilisateur lance les tests, déroule la **vérification manuelle** de la brique, puis merge.

Conventions :
- Une branche par brique (voir §4).
- Deux commits par brique au minimum : `test(<scope>): …` (phase rouge, tests qui échouent) puis `feat(<scope>): …` (phase verte + refactor). Messages en français, comme l'historique. **Le commit rouge est conservé** dans l'historique (pas de squash), pour que le cycle TDD reste visible.
- `go test -race ./...` sans base réelle (SPEC §9). Les dépendances sont simulées par des fakes écrits à la main (pas de bibliothèque de mock).
- Une brique ne modifie le code d'une brique précédente que si un test l'exige, et ce test est alors ajouté.
- Chaque brique se termine par sa **Definition of Done** (DoD) : `make test` et `make lint` verts, cases cochées.
- Chaque brique a une section **Vérification manuelle**, avec des commandes copiables et le résultat attendu de chacune. Le moyen dépend de la brique (D9, D10) :

| Briques | Moyen de vérification manuelle |
|---|---|
| B1 à B3 | exemples exécutables `ExampleXxx` (`go test -run Example -v`) |
| B4 | `make run` + `curl` (routes du socle, déjà branchées sur PostgreSQL) |
| B5, B6 | `make run-memory` + `curl` (stockage en mémoire, sans base) |
| B7, B8 | `make reset` + `curl` + `make psql`, recette `docs/RECETTE.md` |

**Exception assumée : le SQL.** La SPEC (§9) exclut les tests contre un PostgreSQL réel en v1. Pour les briques repository (B7, B8), la phase rouge porte donc sur ce qui est testable sans base (traduction des erreurs, construction des requêtes) et sur la **recette manuelle V1 à V10** (§11), écrite *avant* le code et rejouée à la fin. Leur automatisation relève de l'exercice de validation (§12).

---

## 2. Vue d'ensemble des briques

```
B1 Domaine + normalisation ──► B2 Validation ──► B3 Service (fake repo)
                                                        │
B4 Socle HTTP (middlewares) ──► B5 Handlers écriture ──►│──► B6 Handlers lecture/actions
                                                        │
                                   B7 Repository écritures ──► B8 Repository lecture + câblage + recette
```

| # | Brique | Paquets | Couche testée par |
|---|---|---|---|
| B1 | Domaine et normalisation | `domain`, `service` | tests unitaires purs |
| B2 | Validation des entrées | `service` | tests unitaires purs |
| B3 | Service et orchestration | `service`, `repository/memory` | repository en mémoire |
| B4 | Socle HTTP : middlewares et Problem Details | `httpapi` | `httptest` |
| B5 | Handlers : création, lecture, remplacement + mode mémoire | `httpapi`, `config`, `cmd` | `httptest` + fake service |
| B6 | Handlers : suppression, restauration, tags, listing | `httpapi` | `httptest` + fake service |
| B7 | Repository PostgreSQL : écritures | `repository/postgres` | unitaires (erreurs) + recette |
| B8 | Repository : lecture, câblage, fixture, recette | `repository/postgres`, `cmd`, `scripts` | recette V1–V10 + critères §11.3 |

Ordre justifié : on construit de l'intérieur vers l'extérieur (domaine → service → HTTP), chaque couche étant testée contre une simulation de la couche suivante. Le SQL arrive en dernier, quand les contrats (interfaces) sont figés par les tests.

---

## B1. Domaine et normalisation

**Objectif.** Poser le vocabulaire commun (types, erreurs) et les fonctions de normalisation pures du §4.5.

**Fichiers.**
- `internal/domain/dataset.go` : `Dataset`, `Format` + constantes + `Formats()` (ordre de la SPEC, pour le message d'erreur), `ListQuery { Tags []string; IncludeDeleted bool }`.
- `internal/domain/errors.go` : `ErrNotFound`, `ErrConflict`, `ErrNotDeleted`, `ValidationError { Errors []FieldError }` avec `FieldError { Field, Message string }`.
- `internal/service/normalize.go` : `NormalizeName(string) string`, `NormalizeTag(string) (string, error)`.

**Décision (D1).** Type de l'identifiant : `github.com/google/uuid`, passé de dépendance indirecte à directe. `uuid.UUID` est un `[16]byte` que pgx v5 scanne directement ; aucun type de pgx (`pgtype.UUID`) ne sort de la couche repository.

**Phase rouge : tests à écrire.**
- `NormalizeName` : NFC (forme composée et décomposée de `é` donnent le même résultat), `TrimSpace`, casse conservée, blancs internes conservés.
- `NormalizeTag`, normalisation :
  - `Santé` → `santé` ; `santé` → `santé` (NFC) ;
  - `"  a \t  b  "` → `"a b"` ;
  - ordre des étapes : NFC avant `Fields`, `ToLower` en dernier.
- `NormalizeTag`, validation (après normalisation) :
  - longueur : 1 et 50 runes acceptées, 0 et 51 refusées, `santé` × 10 compté en runes et non en octets ;
  - caractères : lettres (L), chiffres (Nd), `-`, `_`, espace interne acceptés ; ponctuation, emoji, `/`, `.` refusés ;
  - marque combinante (M) acceptée après une lettre sans forme précomposée, **refusée en première position** ;
  - chaîne vide ou uniquement blanche → erreur.
- `ValidationError.Error()` lisible ; `errors.Is` fonctionne sur les sentinelles enveloppées (`fmt.Errorf("…: %w", ErrNotFound)`).
- `Format` : chaque valeur de §4.4 valide, `CSV` (majuscules) et `""` invalides.

**Phase verte.** `golang.org/x/text/unicode/norm` passe en dépendance directe. `unicode.Is(unicode.L / Nd / M, r)`.

**Vérification manuelle.**
- `go test -run Example -v ./internal/service/` : exemples `ExampleNormalizeTag` (`"  Big   DATA "` → `big data`, `santé` → `santé`, `"é-x"` accepté, `"́a"` refusé) et `ExampleNormalizeName`.
- Pour essayer une autre entrée : copier un exemple, changer la chaîne et la ligne `// Output:`, relancer.

**DoD.**
- [ ] Toutes les longueurs mesurées avec `utf8.RuneCountInString`.
- [ ] `internal/domain` n'importe rien hors bibliothèque standard (et le type UUID retenu).

---

## B2. Validation des entrées

**Objectif.** Valider et normaliser un corps de création/remplacement (§5.3, §5.4) et produire une `ValidationError` qui liste **toutes** les erreurs par champ.

**Fichiers.**
- `internal/service/input.go` : `DatasetInput` (champs optionnels en pointeurs pour distinguer absent/`null` de la valeur zéro : `Name *string`, `Format *string`, `SizeBytes *int64`, `Tags []string`…), `validateInput(DatasetInput) (domain.Dataset, error)`.
- `internal/service/sourceurl.go` : `validateSourceURL(string) error`.

**Phase rouge : tests à écrire.**
- Obligatoires : `name` absent ou `null` → erreur `name` ; idem `format`. Les deux manquants → **deux** `FieldError`.
- `name` : 1 à 200 runes **après** normalisation (`"   "` → invalide) ; 200 runes accentuées acceptées.
- `description` ≤ 5 000, `owner` ≤ 200, `license` ≤ 100 runes ; `null` → `nil`.
- `size_bytes`, `row_count` : `0` accepté, `-1` refusé, `null` → `nil`.
- `format` hors liste → message listant `csv, json, parquet, xlsx, xml, geojson, other`.
- `tags` :
  - `null` ou absent → `[]` (jamais `nil` en sortie) ;
  - doublons après normalisation (`France`, `france `) dédupliqués silencieusement ;
  - 20 tags distincts acceptés, 21 refusés ; **la limite s'applique après déduplication** ;
  - un tag invalide → `FieldError` avec un champ indexé (`tags[3]`).
- `source_url` (table de cas) :
  - valides : `https://example.org/x.csv`, `HTTPS://example.org` (schéma insensible à la casse, valeur stockée telle quelle), `s3://bucket/key`, `gs://bucket/o`, `ftp://h/f`, `file:///data/x.csv`, `file://localhost/data/x.csv` ;
  - invalides : `example.org/x` (pas de schéma), `mailto:a@b`, `https:///x` (hôte vide), `s3:///key`, `file://serveur/x`, `file:///` et `file://` (chemin vide), `file:relatif`.

**Décision (D2).** Les `FieldError` sont émises dans **l'ordre des champs du schéma** (`name`, `description`, `source_url`, `format`, `size_bytes`, `row_count`, `owner`, `license`, puis `tags[i]` par index croissant), avec des messages **en français** comme l'exemple du §5.10. Les tests comparent donc la liste complète, ordre compris.

**Vérification manuelle.**
- `go test -run Example -v ./internal/service/` : un exemple qui affiche toutes les `FieldError` d'une entrée volontairement fausse (nom vide, format `pdf`, `size_bytes` négatif, `source_url` sans hôte, 21 tags), dans l'ordre D2, et un exemple de `source_url` valides et invalides.
- `go test -race -run 'TestValidate' -v ./internal/service/` : liste des cas de la table.

**DoD.**
- [ ] `validateInput` est pure (aucun accès au repository).
- [ ] La sortie est déjà normalisée (nom et tags). Le tri des tags n'est pas fait ici : il est garanti par le SQL à la lecture (§4.5).

---

## B3. Service et orchestration

**Objectif.** Définir les deux contrats du projet et les règles métier qui en dépendent.

**Fichiers.**
- `internal/service/repository.go` : interface `DatasetRepository` (définie côté service, §3).
- `internal/service/service.go` : interface `DatasetService` + implémentation.
- `internal/repository/memory/memory.go` : repository en mémoire (D10), **hors** des fichiers `_test.go` pour être réutilisé par `make run-memory` en B5. Protégé par un `sync.Mutex`, car le serveur HTTP l'appellera en concurrence. Il n'importe que `domain` (pas de cycle avec `service`).
- `internal/repository/memory/memory_test.go` : ses propres tests (unicité, soft delete, limite de tags).

**Contrats proposés** (à figer dans cette brique, ils conditionnent B5 à B8) :

```go
type DatasetRepository interface {
    Create(ctx context.Context, d domain.Dataset) (domain.Dataset, error)
    Get(ctx context.Context, id uuid.UUID, includeDeleted bool) (domain.Dataset, error)
    Replace(ctx context.Context, d domain.Dataset) (domain.Dataset, error)
    SoftDelete(ctx context.Context, id uuid.UUID) error
    Restore(ctx context.Context, id uuid.UUID) (domain.Dataset, error)
    AddTag(ctx context.Context, id uuid.UUID, tag string) error
    RemoveTag(ctx context.Context, id uuid.UUID, tag string) error
    List(ctx context.Context, q domain.ListQuery) ([]domain.Dataset, error)
}
```

`DatasetService` expose les mêmes opérations, mais avec des entrées brutes (`DatasetInput`, tag non normalisé) et une méthode de construction de `ListQuery` (voir point de décision).

**Repository en mémoire.** Doit se comporter comme le futur repository PostgreSQL. Il reproduit : unicité insensible à la casse **y compris parmi les supprimés** (`ErrConflict`), renommage avec son propre nom autorisé, `ErrNotFound` sur supprimé (sauf `includeDeleted`), `ErrNotDeleted` sur restauration d'un actif, limite de 20 tags à l'ajout unitaire, horloge injectable pour vérifier `updated_at`.

**Phase rouge : tests à écrire.**
- `Create` : entrée invalide → `ValidationError` **sans appel au repository** ; nom déjà pris (autre casse, dataset supprimé) → `ErrConflict`.
- `Replace` : sémantique `PUT` (champ absent → `null`, `tags` absent → `[]`) ; renommage vers le nom d'un autre → `ErrConflict` ; vers son propre nom en autre casse → OK ; inexistant ou supprimé → `ErrNotFound`.
- `Delete` puis `Get` → `ErrNotFound` ; `Get(includeDeleted=true)` → OK avec `DeletedAt` renseigné ; double suppression → `ErrNotFound`.
- `Restore` : inexistant → `ErrNotFound`, actif → `ErrNotDeleted`, supprimé → dataset avec ses tags d'origine.
- `AddTag` / `RemoveTag` : tag normalisé avant l'appel ; tag invalide → `ValidationError` **même si le dataset est supprimé** (priorité 422 > 404, §5.2) ; ajout idempotent ; 21ᵉ tag → `ValidationError`.
- Règle `updated_at` : avance à chaque écriture réussie, même sans changement de valeur (ajout d'un tag déjà présent compris).
- Propagation : une erreur inattendue du repository remonte enveloppée, `errors.Is` reste vrai pour les sentinelles.

**Décision (D3) : limite de 20 tags à l'ajout unitaire.** Elle dépend de l'état en base : c'est le **repository** qui la vérifie, dans la transaction de `AddTag`, après avoir verrouillé la ligne du dataset (`FOR UPDATE`). Il renvoie alors une `domain.ValidationError` sur le champ `tag`. Le fake reproduit ce comportement. Le service ne relit pas le dataset avant d'écrire, ce qui évite qu'une lecture suivie d'une écriture laisse passer un 21ᵉ tag lors d'ajouts concurrents.

**Décision (D4) : validation des tags du listing.** Un tag invalide dans `?tag=` donne `400`, pas `422` (§5.8). Le service expose `NewListQuery(tags []string, includeDeleted bool) (domain.ListQuery, error)`, qui normalise, déduplique et limite à 20 les tags. Il renvoie une **erreur dédiée** (`domain.QueryError`, distincte de `ValidationError`), que le handler mappe en `400`. Le handler reste ainsi sans logique métier.

Tests supplémentaires découlant de D4 : `NewListQuery` avec 21 tags, `""`, tag invalide → `QueryError` ; `errors.As(err, &ValidationError{})` est **faux** pour une `QueryError`.

**Vérification manuelle.**
- `go test -run Example -v ./internal/service/` : un exemple de scénario complet sur le repository mémoire. On crée `Hôpitaux`, puis on tente de créer `HÔPITAUX` (conflit). On supprime, on relit (introuvable), on restaure, on ajoute un tag déjà présent. Chaque étape affiche son résultat et `updated_at`.
- `go test -race -v ./internal/repository/memory/` : comportement du repository mémoire seul.

**DoD.**
- [ ] Le service ne connaît ni HTTP ni pgx.
- [ ] Assertion de compilation dans les tests du service : `var _ service.DatasetRepository = (*memory.Repository)(nil)`.
- [ ] `go test -race` vert avec des appels concurrents au repository mémoire (un test lance plusieurs `AddTag` en parallèle).

---

## B4. Socle HTTP : middlewares et Problem Details

**Objectif.** Tout ce qui enveloppe les handlers, avant d'en écrire un seul (§5.10, §7.4).

**Fichiers.**
- `internal/httpapi/middleware.go` : request ID, log d'accès, recover, réécriture des 404/405 du mux.
- `internal/httpapi/problem.go` : ajout de `request_id` et `errors[]` ; fonction `writeError(w, r, err)` qui mappe les erreurs domaine.
- `internal/httpapi/router.go` : chaîne de middlewares.
- Tests : `middleware_test.go`, `problem_test.go`, `health_test.go` (les healthchecks du squelette n'ont pas encore de tests).

**Phase rouge : tests à écrire.**
- Request ID :
  - `X-Request-ID: abc-123_.X` réutilisé et renvoyé ;
  - absent, vide, 129 caractères, ou contenant ` `, `/`, `é` → remplacé par 32 caractères hex ;
  - 128 caractères valides → conservé ;
  - présent dans le contexte, dans le Problem Details et dans le log.
- Recover : un handler qui panique → `500` Problem Details `internal-error`, détail générique, log `error` avec stack (logger vers un `bytes.Buffer`).
- Log d'accès : méthode, chemin, statut, durée, taille, request ID (vérifier les clés JSON).
- 404/405 du mux : route inconnue → `404` `application/problem+json` ; mauvaise méthode → `405` avec header `Allow` **conservé** ; le corps `text/plain` d'origine n'est pas émis.
- Mapping erreurs : `ErrNotFound` → 404, `ErrConflict` → 409, `ErrNotDeleted` → 409, `ValidationError` → 422 avec `errors[]`, erreur quelconque → 500 sans fuite du message.
- Healthchecks : `/healthz` → 200 ; `/readyz` avec un `Pinger` en échec → 503 Problem Details ; après `SetShuttingDown` → 503.

**Piège connu.** Pour réécrire un 404/405 du mux, le wrapper doit intercepter `WriteHeader` **avant** que le corps ne parte, et ne réécrire que les réponses du mux (pas un 404 Problem Details émis par un handler). Indice : ne réécrire que si le `Content-Type` posé n'est pas `application/problem+json`.

**Vérification manuelle** (`make run` dans un terminal, les commandes dans un autre) :
- `curl -i localhost:8080/healthz` → `200`, `{"status":"ok"}`, header `X-Request-ID` de 32 caractères hex.
- `curl -i localhost:8080/readyz` → `200` ; après `podman compose stop db`, relancer la commande → `503` en `application/problem+json`.
- `curl -i localhost:8080/nope` → `404` Problem Details avec `request_id`.
- `curl -i -X POST localhost:8080/healthz` → `405`, header `Allow: GET, HEAD`, Problem Details.
- `curl -i -H 'X-Request-ID: essai-1' localhost:8080/nope` → `request_id` vaut `essai-1` ; avec `X-Request-ID: pas valide!` → remplacé.
- Les logs JSON de l'API affichent une ligne d'accès par requête, avec le même request ID.

**DoD.**
- [ ] Ordre des middlewares documenté dans `router.go` (request ID le plus à l'extérieur, puis log, puis recover, puis réécriture 404/405).
- [ ] Critère d'acceptation 5 (§11.3) couvert par les tests.

---

## B5. Handlers : création, lecture, remplacement

**Objectif.** `POST /datasets`, `GET /datasets/{id}`, `PUT /datasets/{id}`, avec le décodage strict commun.

**Fichiers.**
- `internal/httpapi/decode.go` : contrôle `Content-Type` (§5.9), `http.MaxBytesReader` (1 Mio), `DisallowUnknownFields`, rejet des valeurs multiples, corps vide ; `parseID`.
- `internal/httpapi/dto.go` : `datasetResponse` (tous les champs, `null` explicites, `tags: []`, dates `time.RFC3339` en UTC).
- `internal/httpapi/datasets.go` : handlers.
- `internal/httpapi/fake_service_test.go`.
- **Mode mémoire (D10)** :
  - `internal/config` : nouvelle variable `STORAGE_BACKEND` (`postgres` par défaut, ou `memory`). En `memory`, `DATABASE_URL` n'est plus obligatoire. Les tests de config couvrent ces deux cas et une valeur invalide.
  - `cmd/datacat/main.go` : branche repository → service → router. En `memory`, pas de pool ni de migrations, et `/readyz` utilise un `Pinger` toujours OK. En `postgres`, le repository PostgreSQL est un **squelette** dont chaque méthode renvoie une erreur « non implémenté » (→ `500`) jusqu'à B7.
  - `Makefile` : cible `run-memory` (`STORAGE_BACKEND=memory go run ./cmd/datacat`, sans `.env` ni Podman).
  - `SPEC.md` : ajouter `STORAGE_BACKEND` au §6 et `run-memory` au §10, en précisant qu'il s'agit d'un mode de développement (données perdues à l'arrêt).

**Phase rouge : tests à écrire.**
- `Content-Type` : `application/json` et `application/json; charset=UTF-8` acceptés ; absent, `text/plain`, `application/json; charset=latin1`, `application/json; foo=bar` → 415.
- Décodage : JSON invalide, corps vide, `{}{}`, champ inconnu (`id`, `created_at`, `foo`) → 400 ; corps > 1 Mio → 413.
- **Priorité des erreurs** (§5.2) : corps trop gros *et* mauvais `Content-Type` → 413 ; mauvais `Content-Type` *et* id invalide → 415 ; id invalide *et* corps invalide → 400 ; corps valide syntaxiquement mais invalide métier sur un id inexistant → 422 (le fake service le garantit).
- `POST` : 201, header `Location: /api/v1/datasets/{id}`, corps complet ; erreurs 409 et 422 en Problem Details.
- `GET {id}` : 200 ; id non UUID → 400 ; `?include_deleted=true` transmis au service ; valeur invalide → 400.
- `PUT` : 200, corps complet ; 404, 409, 422.
- Format de sortie : dates sans fraction et suffixe `Z` même si l'heure du fake a des nanosecondes et un fuseau non UTC ; `"tags": []` jamais `null` ; champs absents en `null`.

**Décision (D5).** Pas de DTO d'entrée dans `httpapi` : le corps est décodé directement dans `service.DatasetInput`, qui porte des tags `json:"…"` (pointeurs = `null` ou absent). Cela évite un second type et une copie champ à champ. Garde-fou : le service n'importe pas `encoding/json`, les tags de struct ne sont que des métadonnées. Le DTO de **sortie** (`datasetResponse`) reste dans `httpapi`, car le format des dates lui est propre.

**Vérification manuelle** (`make run-memory`, puis dans un autre terminal) :

```sh
API=localhost:8080/api/v1/datasets
J='Content-Type: application/json'
curl -i -H "$J" -d '{"name":"Population 2024","format":"csv","tags":["Santé","  big   data "]}' $API
#   201, header Location, tags ["big data","santé"], dates sans fraction, champs absents à null
ID=<id renvoyé>
curl -s $API/$ID | jq                                                     # 200, même corps
curl -i -H "$J" -d '{"name":"POPULATION 2024","format":"csv"}' $API       # 409
curl -i -H "$J" -d '{"name":"","format":"pdf"}' $API                      # 422, errors[] dans l'ordre D2
curl -i -H "$J" -d '{"name":"x","format":"csv","id":"…"}' $API            # 400, champ inconnu
curl -i -d '{"name":"x","format":"csv"}' $API                             # 415 (Content-Type de curl -d : form-urlencoded)
curl -i -X PUT -H "$J" -d '{"name":"Population 2024","format":"json"}' $API/$ID   # 200, tags [] et description null
curl -i $API/pas-un-uuid                                                  # 400
```

**DoD.**
- [ ] Aucune règle métier dans les handlers (pas de normalisation, pas de longueur).
- [ ] `decode.go` réutilisable par B6 (vérification « corps vide attendu »).

---

## B6. Handlers : suppression, restauration, tags, listing

**Objectif.** Les cinq routes restantes de §5.2.

**Fichiers.** `internal/httpapi/datasets.go` (suite), `internal/httpapi/query.go` (parsing du listing), tests associés.

**Phase rouge : tests à écrire.**
- `DELETE {id}` : 204 sans corps ; 404 ; corps non vide → 400 ; `Content-Type` quelconque ignoré.
- `POST {id}/restore` : 200 + corps ; 404 ; 409 (non supprimé) ; corps non vide → 400.
- `POST|DELETE {id}/tags/{tag}` : 204 ; tag URL-encodé (`sant%C3%A9`, `sante%CC%81`, `big%20data`) transmis décodé ; tag invalide sur dataset supprimé → **422** (pas 404) ; 21ᵉ tag → 422 ; corps non vide → 400.
- `GET /datasets` :
  - sans paramètre → `{"items": [...]}`, `items` vaut `[]` et non `null` si vide ;
  - `?tag=a&tag=b` → deux tags transmis ;
  - 400 pour : 21 `tag`, `?tag=`, tag invalide, `include_deleted=TRUE|1|` (vide), `include_deleted` répété, paramètre inconnu (`?q=x`, `?limit=10`).
- Routes et méthodes : `PATCH {id}` → 405 avec `Allow: GET, PUT, DELETE` (ordre indifférent dans l'assertion) ; `GET {id}/restore` → 405.

**Vérification manuelle** (`make run-memory`, avec deux datasets créés comme en B5) :

```sh
curl -i -X POST $API/$ID/tags/Sant%C3%A9                     # 204
curl -s "$API?tag=sante%CC%81" | jq '.items[].name'          # le dataset, grâce à NFC
curl -s "$API?tag=santé&tag=climat" | jq '.items | length'   # OU entre les tags
curl -i "$API?tag="                                          # 400
curl -i "$API?include_deleted=TRUE"                          # 400
curl -i "$API?q=x"                                           # 400, paramètre inconnu
curl -i -X DELETE $API/$ID                                   # 204
curl -i $API/$ID                                             # 404
curl -s "$API/$ID?include_deleted=true" | jq .deleted_at     # date renseignée
curl -i -X POST $API/$ID/tags/a.b                            # 422 (tag invalide, même supprimé)
curl -i -X POST $API/$ID/restore                             # 200, tags d'origine
curl -i -X POST $API/$ID/restore                             # 409
curl -i -X PATCH $API/$ID                                    # 405 + Allow
```

**DoD.**
- [ ] Toutes les routes du §5.2 enregistrées ; la table des routes est lisible d'un coup d'œil dans `router.go`.
- [ ] Les 400 du listing et les 422 des tags unitaires sont bien distingués.

---

## B7. Repository PostgreSQL : écritures

**Objectif.** Implémenter les écritures de `DatasetRepository` en SQL écrit à la main, avec transactions pour les opérations multi-tables.

**Fichiers.**
- `internal/repository/postgres/datasets.go` : `Create`, `Replace`, `SoftDelete`, `Restore`, `AddTag`, `RemoveTag`.
- `internal/repository/postgres/errors.go` : `translateError(error) error`.
- `scripts/seed.sql` : fixture §11.1.
- `docs/RECETTE.md` : la recette V1 à V10 sous forme de requêtes `curl` et de résultats attendus, à cocher.

**Phase rouge.**
- Tests unitaires de `translateError` (sans base) : `*pgconn.PgError{Code: "23505"}` → `ErrConflict` ; `pgx.ErrNoRows` → `ErrNotFound` ; enveloppé → toujours traduit ; autre code → erreur d'origine conservée.
- Assertion de compilation : `var _ service.DatasetRepository = (*Repository)(nil)`.
- Écriture de `seed.sql` et de `docs/RECETTE.md` **avant** le code SQL : c'est la spécification exécutable de B7 et B8. À ce stade, `make reset` charge la fixture mais les cas V échouent (routes non câblées).

**Phase verte : points d'attention SQL.**
- `Create` / `Replace` : une transaction ; `INSERT INTO tags … ON CONFLICT (name) DO NOTHING` puis lecture des `id`.
- `Replace`, **décision (D6)** : les liaisons sont remplacées en bloc, avec `DELETE FROM dataset_tags WHERE dataset_id = $1` puis `INSERT` de la nouvelle liste, dans la même transaction. Il n'y a pas de calcul de différence : une seule logique, pour au plus 20 lignes.
- `AddTag` (D3) : `SELECT … FOR UPDATE` sur le dataset, comptage des liaisons, `ValidationError` si l'ajout d'un tag **absent** ferait passer à 21. Un tag déjà présent reste un `204`, même s'il y a 20 tags.
- `updated_at = date_trunc('second', now())` dans **chaque** `UPDATE`, y compris `AddTag`/`RemoveTag` sans effet sur les liaisons.
- `Replace`, `SoftDelete`, `AddTag`, `RemoveTag` : `WHERE id = $1 AND deleted_at IS NULL` ; zéro ligne → `ErrNotFound`.
- `Restore` : distinguer inexistant (`ErrNotFound`) de non supprimé (`ErrNotDeleted`) ; `SELECT … FOR UPDATE` dans la transaction.
- Relecture du dataset complet (tags `ORDER BY name COLLATE "C"`) en fin de `Create`, `Replace`, `Restore` : mutualiser avec le `Get` de B8.

**Vérification manuelle.**
- `make reset` (**efface les données**), puis les `curl` d'écriture de B5 et B6 contre `make up` : `POST`, `PUT`, `DELETE`, `restore`, ajout et retrait de tags.
- `make psql` pour constater l'effet en base :
  - `SELECT name, updated_at, deleted_at FROM datasets ORDER BY created_at DESC;` → `updated_at` à la seconde, avance après chaque écriture ;
  - `SELECT * FROM tags;` → tags normalisés, pas de doublon, tags orphelins conservés après un `PUT` ;
  - `SELECT count(*) FROM dataset_tags WHERE dataset_id = '…';` → liaisons conservées après `DELETE`, remplacées après `PUT`.
- Les `GET` répondent `500` tant que B8 n'est pas fait (lecture non implémentée) : c'est attendu.

**DoD.**
- [ ] Aucune concaténation de valeurs dans le SQL (paramètres `$n` uniquement).
- [ ] `seed.sql` fixe les `created_at` et les `id` de D6 et D8 (§11.1).

---

## B8. Repository : lecture, câblage et recette

**Objectif.** Terminer le repository, câbler les couches dans `main`, dérouler la recette et les critères d'acceptation.

**Fichiers.** `internal/repository/postgres/datasets.go` (`Get`, `List`). Le câblage de `main` existe depuis B5 : il n'y a plus qu'à retirer les derniers « non implémenté ».

**Phase rouge.**
- Tests unitaires de ce qui est testable sans base (ex. construction des arguments de `List` si une fonction dédiée existe).
- `make reset` puis dérouler `docs/RECETTE.md` : les cas de lecture et de listing échouent (`500`) tant que `Get` et `List` ne sont pas implémentés.

**Phase verte : points d'attention SQL.**
- `List` sans tag : `WHERE deleted_at IS NULL` (sauf `include_deleted`), `ORDER BY created_at DESC, id DESC`.
- `List` avec tags (OU) : `WHERE EXISTS (SELECT 1 FROM dataset_tags dt JOIN tags t … WHERE t.name = ANY($1))`, ce qui évite les doublons sans `DISTINCT`.
- Agrégation des tags : `coalesce(array_agg(t.name ORDER BY t.name COLLATE "C") FILTER (WHERE t.name IS NOT NULL), '{}')` en `LEFT JOIN`, ou sous-requête corrélée ; vérifier qu'un dataset sans tag renvoie `[]`.
- Pas de N+1 : une requête pour le listing complet.

**Recette (§11) = vérification manuelle de B8.** Chaque cas part d'un `make reset`, sauf V10 qui suit V9.
- [ ] V1, V1b, V2, V3, V4, V5, V6, V6b, V7, V8, V9, V10.
- [ ] Critères d'acceptation 1 à 7 du §11.3 (redémarrage sans perte, migrations idempotentes, `SIGTERM` → code 0, `make lint` et `make test` verts).

**DoD.**
- [ ] `docs/RECETTE.md` entièrement coché.
- [ ] Merge de `feat/skeleton` puis branches de briques vers `main`.

---

## 3. Décisions actées

Décisions prises lors de l'entretien du 2026-10-30. Celles qui précisent la spec (D3, D4) sont à reporter dans `SPEC.md` au moment de la brique concernée.

| # | Brique | Question | Décision | Alternative écartée |
|---|---|---|---|---|
| D1 | B1 | Type d'identifiant | `github.com/google/uuid` | type maison, `pgtype.UUID` (fuite de pgx hors du repository) |
| D2 | B2 | Ordre et langue des messages de validation | ordre du schéma, français | ordre alphabétique, anglais |
| D3 | B3, B7 | Où vérifier la limite de 20 tags à l'ajout unitaire | repository, transaction avec `FOR UPDATE` | lecture puis écriture dans le service (course), trigger (exclu par §4.1) |
| D4 | B3, B6 | Signalement d'un tag invalide dans le listing | `NewListQuery` + `domain.QueryError` → `400` | normalisation par le handler, `ValidationError` portant sa source |
| D5 | B5 | Type du corps JSON en entrée | `service.DatasetInput` avec tags `json` | DTO dédié dans `httpapi` |
| D6 | B7 | Mise à jour des liaisons au `PUT` | `DELETE` + `INSERT` dans la transaction | calcul de différence |
| D7 | toutes | Organisation Git | une branche par brique | branche unique `feat/v1`, commits directs sur `main` |
| D8 | toutes | Commits de la phase rouge | conservés, sans squash | squash au merge, commit unique par brique |
| D9 | B1 à B3 | Vérification manuelle sans HTTP | exemples exécutables `ExampleXxx` | CLI de démo, tests verbeux seuls |
| D10 | B3, B5, B6 | Essais `curl` avant le repository PostgreSQL | `internal/repository/memory` + `STORAGE_BACKEND=memory` + `make run-memory` (**ajout à la SPEC**) | attendre B8, réordonner les briques |
| D11 | toutes | Points d'arrêt pour la vérification | après la phase rouge et en fin de brique | fin de brique seulement, après chaque phase |
| D12 | toutes | Commits et merges | Claude crée les branches et commite, l'utilisateur merge | l'utilisateur commite, Claude merge |

## 4. Branches et historique

Une branche par brique, partant de `main` après merge du squelette (`feat/skeleton`) : `feat/b1-domaine`, `feat/b2-validation`, `feat/b3-service`, `feat/b4-socle-http`, `feat/b5-handlers-ecriture`, `feat/b6-handlers-actions`, `feat/b7-repo-ecritures`, `feat/b8-repo-lecture-recette`. Chaque branche est mergée dans `main` **par l'utilisateur**, après sa vérification (D12), avant de créer la suivante (circuit court).

Chaque branche contient au moins le commit rouge (`test(…)`) et le commit vert (`feat(…)`), mergés **sans squash** (D8). Conséquence assumée : le commit rouge laisse `make test` en échec. Lors d'un `git bisect`, on l'ignore avec `git bisect skip`. Pour les repérer facilement, on utilise le préfixe `test(` : `git log --oneline --grep '^test('`.
