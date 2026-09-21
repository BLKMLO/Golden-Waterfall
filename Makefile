# Golden Waterfall — tâches de développement et de publication.
#
# Le programme est UN binaire sans dépendance native : la compilation
# croisée ne demande ni chaîne C ni bibliothèque à installer.

BINARY  := gw
PKG     := ./cmd/gw
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.Version=$(VERSION)
DIST    := dist

.PHONY: help build run test race cover vet fmt lint clean dist linux windows macos all

help: ## Affiche cette aide
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Compile le binaire pour la machine courante
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

run: build ## Compile puis lance l'interface
	./$(BINARY)

test: ## Suite de tests
	go test ./...

race: ## Suite de tests avec détecteur de concurrence
	go test -race ./...

cover: ## Couverture de tests (rapport HTML)
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Rapport : coverage.html"

vet: ## Analyse statique
	go vet ./...

fmt: ## Reformate le code
	gofmt -w cmd internal

lint: fmt vet ## Reformate puis analyse

clean: ## Supprime les artefacts de compilation
	rm -rf $(BINARY) $(BINARY).exe $(DIST) coverage.out coverage.html $(SYSO)

# --- Publication ----------------------------------------------------------

dist: linux windows macos ## Compile pour les trois systèmes
	@ls -lh $(DIST)

linux: ## Binaire Linux (amd64 + arm64)
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64 $(PKG)

macos: ## Binaire macOS (Intel + Apple Silicon)
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64 $(PKG)

# Icône Windows : Go n'embarque pas d'icône nativement. La méthode
# standard consiste à générer un fichier de ressources .syso que la chaîne
# de compilation ramasse AUTOMATIQUEMENT s'il est présent à la racine du
# paquet main.
#
# Il n'y a donc RIEN à décommenter : déposer build/icon.ico (256×256
# recommandé) suffit. La cible le détecte, et dit clairement ce qui manque
# plutôt que de produire un binaire sans icône en silence.
#
#   go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
ICON  := build/icon.ico
SYSO  := cmd/gw/resource_windows_amd64.syso

windows: $(SYSO) ## Binaire Windows (amd64, icône si build/icon.ico existe)
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-windows-amd64.exe $(PKG)

.PHONY: $(SYSO)
$(SYSO):
	@if [ ! -f $(ICON) ]; then \
		echo "Icône : $(ICON) absent — binaire Windows SANS icône (c'est normal tant qu'aucune icône n'existe)."; \
	elif ! command -v goversioninfo >/dev/null 2>&1; then \
		echo "Icône : $(ICON) présent mais goversioninfo introuvable — binaire SANS icône."; \
		echo "  go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest"; \
	else \
		goversioninfo -icon=$(ICON) -o $(SYSO) && echo "Icône embarquée depuis $(ICON)."; \
	fi

all: lint race dist ## Tout : format, analyse, tests, binaires
