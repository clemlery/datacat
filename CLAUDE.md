# CLAUDE.md : datacat

API REST en Go (catalogue de *datasets*), PostgreSQL 18, Podman rootless.

## Documents de référence

- `docs/SPEC.md` est la **source de vérité** fonctionnelle et technique. En cas de doute, la spec l'emporte sur le code. Ne jamais s'en écarter sans le dire ; si un écart est décidé, mettre la spec à jour dans la même brique.
- `docs/PLAN.md` découpe le travail en briques (B1 à B8) et liste les décisions actées (D1 à D12). Ne pas rouvrir une décision actée sans raison nouvelle.
- `docs/RECETTE.md` (créé en B7) : recette manuelle V1 à V10 de la spec §11.

## Commandes

| Commande | Usage |
|---|---|
| `make test` | `go test -race ./...`, sans base réelle |
| `go test -race -run <Regex> -v ./internal/<pkg>/` | une partie des tests, en verbeux |
| `go test -run Example -v ./internal/<pkg>/` | exemples exécutables (vérification manuelle B1 à B3) |
| `make lint` | golangci-lint, linters par défaut, **à installer sur l'hôte** |
| `make run` | `db` dans Podman + API en local sur `:8080` |
| `make run-memory` | API en local avec le stockage en mémoire, sans base (à partir de B5) |
| `make up` / `make down` / `make logs` | stack complète dans Podman |
| `make reset` | **destructif** : efface le volume, redémarre, charge la fixture |
| `make psql` | shell psql dans le conteneur `db` |

Outils : `podman compose` (jamais `docker`), `curl` et `jq` pour les essais manuels.

## Architecture

```
httpapi (handlers) ──► service (règles métier) ──► repository (postgres | memory)
        └──────────────► domain (modèles, erreurs) ◄──────────┘
```

- `internal/domain` : types et erreurs sentinelles, bibliothèque standard + `google/uuid` uniquement.
- `internal/service` : validation, normalisation, orchestration. Définit l'interface `DatasetRepository`. Ne connaît ni HTTP ni pgx, n'importe pas `encoding/json`.
- `internal/httpapi` : décodage, encodage, mapping erreurs domaine → Problem Details. **Aucune règle métier.**
- `internal/repository/postgres` : SQL écrit à la main, paramètres `$n` uniquement, transactions pour dataset + tags.
- `internal/repository/memory` : implémentation en mémoire, utilisée par les tests du service et par `make run-memory`.

## Méthode de travail : une brique à la fois, en TDD

Ne travailler que sur la brique en cours, telle que décrite dans `docs/PLAN.md`. Ne pas anticiper la brique suivante.

1. **Branche** : créer `feat/bN-<nom>` depuis `main` à jour (noms dans PLAN §4).
2. **Phase rouge** : écrire les tests de la brique (table-driven), plus le strict nécessaire pour compiler (types, signatures, corps vides). Vérifier que `make test` échoue **sur des assertions**, pas à la compilation. Commit `test(<scope>): …`.
3. **⏸ Arrêt n°1** : présenter la liste des cas de test et attendre la validation de l'utilisateur avant d'implémenter. Intégrer ses remarques (nouveau commit `test(…)` si besoin).
4. **Phase verte puis refactor** : implémenter jusqu'à ce que **tous** les tests passent, puis nettoyer. `make test` et `make lint` verts. Commit `feat(<scope>): …`.
5. **⏸ Arrêt n°2** : rendre le rapport de fin de brique (format ci-dessous) et attendre. **L'utilisateur vérifie et merge lui-même dans `main`.**

Règles Git :
- Claude crée les branches et fait les commits. Claude ne merge jamais dans `main` et ne pousse jamais sans demande explicite.
- Messages de commit en français, préfixes `test(…)`, `feat(…)`, `fix(…)`, `refactor(…)`, `docs(…)`, `chore(…)`.
- Le commit rouge est conservé tel quel (pas de squash, pas d'amend après l'arrêt n°1).

## Vérification par l'utilisateur

Chaque brique doit pouvoir être vérifiée de deux façons : en lançant les tests et à la main. Le mode de vérification manuelle dépend de la brique :

| Briques | Vérification manuelle |
|---|---|
| B1 à B3 | Fonctions `ExampleXxx` avec `// Output:` dans les `_test.go`, lancées par `go test -run Example -v`. L'utilisateur peut en copier une et changer l'entrée. |
| B4 | `make run` puis `curl` sur `/healthz`, `/readyz`, une route inconnue, une mauvaise méthode, `X-Request-ID` |
| B5, B6 | `make run-memory` puis `curl` sur les routes de la brique |
| B7, B8 | `make reset` puis `curl` (et `make psql` pour inspecter les tables), recette `docs/RECETTE.md` |

Chaque brique de `docs/PLAN.md` a une section « Vérification manuelle » avec les commandes exactes. Les tenir à jour si l'implémentation diverge.

### Rapport de fin de brique (arrêt n°2)

```
## Brique BN : <nom> (branche feat/bN-…)

### Fait
- … (fichiers créés ou modifiés, décisions appliquées)

### Tests
make test                       # résultat attendu : ok, N tests
go test -race -run … -v ./…     # pour voir les cas de la brique

### Vérification manuelle
<commandes copiables, chacune suivie du résultat attendu>

### Écarts et points d'attention
- … (écart avec la spec ou le plan, limite connue, ou « aucun »)
```

## Conventions de code

- Commentaires, messages d'erreur et logs **en français** ; identifiants en anglais. Suivre la densité de commentaires du code existant : expliquer le *pourquoi*, pas le *quoi*.
- Longueurs toujours en runes (`utf8.RuneCountInString`), jamais `len()` sur du texte (spec §4).
- Dates : stockées à la seconde, sérialisées en `time.RFC3339` après `.UTC()`, jamais `RFC3339Nano`.
- Erreurs : envelopper avec `%w`, tester avec `errors.Is` / `errors.As`. Ne jamais exposer le détail d'une erreur 500 au client.
- Tests : table-driven, `t.Run` avec des noms lisibles, fakes écrits à la main (pas de bibliothèque de mock), aucun test ne dépend d'une base réelle.
- Pas de nouvelle dépendance sans le signaler à l'utilisateur.

## Pièges connus

- PostgreSQL 18 : volume monté sur `/var/lib/postgresql`, ne pas surcharger `PGDATA`. `POSTGRES_INITDB_ARGS` n'agit qu'à la création du volume.
- `make reset` et `podman compose down -v` effacent les données : **demander avant de les lancer**.
- `.env` n'est pas versionné ; seul `.env.example` l'est.
- `golangci-lint` n'est pas fourni par `go tool` : s'il manque, `make lint` échoue avec un message. Le signaler plutôt que de le contourner.
