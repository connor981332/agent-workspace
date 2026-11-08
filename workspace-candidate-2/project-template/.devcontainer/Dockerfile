# Development image only; production images live elsewhere in the repo.
# Replace the base with the project's toolchain image (ruby, node, python, ...).
# What matters to the workspace:
#   - a non-root user, {{USER}}, that bin/ws and VS Code both run as (USER below)
#   - git and the project's tools; agents run code here through bin/ws
#   - a private root CA, if the project talks to internal hosts:
#       COPY .devcontainer/internal_root_ca.crt /usr/local/share/ca-certificates/
#       RUN update-ca-certificates
FROM mcr.microsoft.com/devcontainers/base:debian

USER {{USER}}
