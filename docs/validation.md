# Validação da migração KEDA

Data: 2026-10-12 (America/Maceio). Ambiente Linux amd64, Go 1.24.7, Helm 3.17.3. Esta revisão substitui o relatório da implementação inicial Pub/Sub.

Resultado: **PASS** em todas as etapas de `make verify`. Testes atualizados foram executados novamente com `make test integration`, também com sucesso.

## Escopo automatizado

`make verify`: geração DeepCopy/CRD, gofmt, go vet, testes unitários/reconciliação com race detector, integração envtest API server 1.32.0 + etcd, build CGO_ENABLED=0, Helm lint e renderização dos manifests.

A integração exercita o endpoint real `/scale`: leitura, alteração somente de requestedReplicas, preservação do restante da spec, conflito com resourceVersion antigo e status com Pods/selector do NodeSet. Confirma diferença entre Pods observados e count já aumentado no ECK. Continua verificando targetRef imutável, bloqueio de scale down, DryRun sem persistência, um único count autorizado, espera de convergência após restart e CAS entre writers concorrentes.

Testes de engine/reconciliação cobrem demanda KEDA menor/maior, pedido zero sem downscale, limites min/max/step, pisos sobrepostos, madrugada/DST, autorização e ECK pendente. Atualizações de demanda não reiniciam estabilização; alteração da política reinicia. Testes de segurança Elasticsearch mantêm cobertura de disco, shards e saúde.

## Limites

Envtest não executa KEDA, HPA, ECK ou Elasticsearch. O teste escreve em /scale com cliente Kubernetes e usa segurança Elasticsearch simulada. Os exemplos ScaledObject não foram exercitados com controladores reais. É necessário homologar métricas, cron, autenticação, permissões, GitOps, provisão de Pods/PVCs e realocação em cluster conforme runbook.

Não executados nesta revisão: imagem Docker local (runtime ausente), publicação de imagem/release e deploy em cluster. Nenhum cluster foi alterado. Não há garantia de freshness das métricas em /scale nem certificação de produção.

## Cobertura adicional de segurança

Acrescentados testes de mapa de owners com falha fechada, dois NodeSets com bloqueio e liberação após convergência, preservação do cooldown do primeiro writer mesmo quando a segunda política tem prazo menor, timeout persistido após restart sem rollback e hosts distintos opt-in. Envtest agora inclui um segundo autoscaler autorizado no mesmo Elasticsearch e verifica que ele não inicia scaling enquanto o primeiro está pendente. A implementação foi gerada, compilada e verificada novamente; não há teste de runtime KEDA/Elasticsearch real.

## KEDA local scenario added on 2026-10-12

Repeated `make verify`: race tests, envtest API integration, build and Helm passed.
`make local-check`: five Python tests passed, including real HTTP metrics serving,
isolated kubectl invocation, fresh-cluster guard, stale-condition rejection and
return to Audit after scenario failure. Compose YAML parsing passed.

Added `compose.yaml` and `hack/local/lab.py` for live KEDA/HPA + ECK testing.
The live Docker scenario has **not** run here: no Docker executable or daemon socket.
See [local-lab.md](local-lab.md) for exact assertions and commands. No production
cluster, cloud credentials or hosted CI result was used as validation evidence.
