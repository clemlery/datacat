# Projet :
une API REST en Go qui gère un catalogue de jeux de données (CRUD, recherche par tag), avec PostgreSQL sous Docker Compose, une architecture en couches (handlers, service, repository) et des migrations SQL.
Je compte utilsier PostgreSQL version 18. Une chose à savoir à propos de cette version : Depuis PostgreSQL 18, la variable PGDATA de l’image dépend de la version : pour la 18 c’est /var/lib/postgresql/18/docker, et le VOLUME déclaré est passé à /var/lib/postgresql.
L'outil de conteneurisation sera Podman rootless avec podman compose.
Pour ce qui est de Go je compte utiliser la version 1.26.
