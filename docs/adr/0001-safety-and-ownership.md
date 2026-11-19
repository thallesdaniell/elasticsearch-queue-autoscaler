# ADR 0001: atuação limitada ao count de Data Nodes

Status: aceito para o MVP, 2026-10-11.

Contexto: queremos ajustar a capacidade de indexação sem reimplementar a orquestração do ECK nem depender do autoscaler Enterprise.

Decisão: operador separado em Go/controller-runtime. CR namespaced; alvo no mesmo namespace. Patch JSON apenas do count selecionado e de annotations de recuperação. Nunca patch da lista inteira, StatefulSet, PVC ou configuração de shards. A API ECK é acessada como Unstructured; nossa CRD é tipada/gerada com controller-gen.

Autorização: RBAC limita nomes de Elasticsearch; annotations declaradas pelo administrador autorizam o nome do autoscaler e NodeSet. Roles precisam estar explícitas, ter data/data_* e não incluir master. A limitação inicial de um NodeSet foi substituída pela ADR 0005: mapa explícito de owners e serialização por convergência. RBAC não restringe campos JSON, portanto usuários que controlam a ServiceAccount/annotations são confiáveis.

Concorrência: Lease global por namespace, reconcile sequencial, leituras não cacheadas e CAS por resourceVersion. Pending-count/last-scale e novo count são persistidos no mesmo patch. Conflito força nova avaliação; não se reaplica uma decisão antiga com retry de patch isolado.

Consequências: um NodeSet com operação em andamento bloqueia o scaling dos demais até convergir. Não existe finalizer porque a remoção do autoscaler não deve reduzir/reverter capacidade; annotations permanecem para auditoria e precisam ser transferidas explicitamente para outro owner.

ArgoCD precisa ignorar count do NodeSet selecionado durante diff e sync. Não ignorar nodeSets inteiro nem delegar a spec inteira. Testar a versão específica do ArgoCD antes de Auto.
