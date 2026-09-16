.DEFAULT_GOAL := help

.PHONY: help deploy deploy-no-pull deploy-assets verify-production production-status

help:
	@printf '%s\n' 'make deploy            # pull, sync assets, build, recreate gateway, verify'
	@printf '%s\n' 'make deploy-no-pull    # deploy current checkout without git pull'
	@printf '%s\n' 'make deploy-assets     # sync only managed Hermes assets and verify them'
	@printf '%s\n' 'make verify-production # safe production checks'
	@printf '%s\n' 'make production-status # Compose and gateway status'

deploy:
	@./scripts/deploy-production.sh

deploy-no-pull:
	@./scripts/deploy-production.sh --no-pull

deploy-assets:
	@sudo ./scripts/deploy-hermes-assets.sh

verify-production:
	@./scripts/verify-production.sh

production-status:
	@./scripts/verify-production.sh --status
