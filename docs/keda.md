# KEDA como provider principal

KEDA coleta sinais externos e fornece métricas ao HPA; KEDA/HPA solicita réplicas na CRD ElasticsearchQueueAutoscaler via `/scale`. O operador não implementa protocolo gRPC external scaler nem endpoint webhook: é o alvo escalável protegido por uma política de segurança.

## Contrato

| Campo | Responsável | Semântica |
|---|---|---|
| spec.provider | Administrador | KEDA, único provider aceito |
| spec.requestedReplicas | KEDA/HPA | Intenção persistida, 0..10000; operador não sobrescreve |
| status.replicas | Operador | Pods não terminando do NodeSet, inclusive ainda não prontos |
| status.readyReplicas | Operador | Pods prontos desse mesmo conjunto |
| status.selector | Operador | Labels cluster-name e statefulset-name, exclusivas do NodeSet |
| status.currentReplicas | Operador | count aplicado no spec do ECK |
| status.desiredReplicas | Operador | Próximo count após limites e composição |

O subrecurso mapeia specReplicasPath=.spec.requestedReplicas, statusReplicasPath=.status.replicas e labelSelectorPath=.status.selector. Status deve ser reconciliado antes de esperar decisões válidas do HPA. Um pedido zero não autoriza remover nós. O exemplo usa minReplicaCount positivo e desabilita scaleDown no HPA; o operador também bloqueia redução independentemente dessa configuração.

## Configuração recomendada

Use um ScaledObject por CR, no mesmo namespace, apontando ao apiVersion/kind da nossa CRD. Alinhe minReplicaCount/maxReplicaCount aos limites do operador. Não use idleReplicaCount, restauração da contagem original nem um segundo HPA no mesmo alvo. O ScaledObject não pode mirar o StatefulSet ECK.

`config/samples/scaledobject.yaml` combina Prometheus e duas janelas cron. Endpoint, métrica e threshold são ilustrativos: substitua e calibre antes de Auto. A query requer uma amostra recente e ignoreNullValues=false; erro/ausência é tratado pelo KEDA, não convertido em zero pelo operador. Falha em um trigger não significa necessariamente bloquear todos os outros: KEDA/HPA conserva sua própria semântica de múltiplas métricas. Não há política de falha global fechada neste contrato.

O exemplo usa AverageValue sobre backlog total para expressar capacidade por nó. Não use uma métrica de réplicas desejadas como se fosse backlog sem revisar metricType e fórmula HPA. Para CPU/memória, seletores, requests dos Pods e métricas devem ser homologados; o exemplo não valida esse caminho.

Para autenticação, configure TriggerAuthentication/ClusterTriggerAuthentication e o método suportado pelo scaler escolhido. Custom resources não oferecem necessariamente o template Pod esperado pela resolução de env do KEDA: prefira parâmetros explícitos/Secret/pod identity, conforme documentação do scaler. Nenhuma credencial cloud precisa ser montada neste operador.

## Horários

Triggers cron do KEDA são o caminho principal. A maior recomendação ganha entre cron e métricas; isso faz o horário funcionar como piso. O operador também mantém `sources.schedules` como piso local opcional. Evite representar a mesma janela nos dois lugares. Fim de janela não reduz capacidade no MVP.

## Pausa, falhas e retomada

`/scale` não transporta timestamp de coleta, validade, heartbeat ou estado de saúde do KEDA. O valor solicitado permanece até outro writer alterá-lo. Mesmo sem KEDA, uma demanda pendente pode continuar sendo aplicada em passos, após cooldown e convergência ECK. Pausar KEDA ou apagar ScaledObject não cancela essa intenção. Use `spec.mode: Audit` para suspender novos patches.

Antes de sair de Audit/DryRun, revise requestedReplicas, HPA, ScaledObject e métricas atuais. Enquanto o operador está em Audit, o HPA pode acumular uma solicitação maior que o count real. Limites e estabilização continuam obrigatórios. Não foi implementado um watchdog de freshness do KEDA; exigiria outro contrato além de /scale.

## RBAC e GitOps

KEDA e o controlador HPA precisam ler/atualizar `/scale`. `config/samples/keda-rbac.yaml` oferece Role por nome do alvo e bindings ilustrativos; confira as identidades efetivas. Instalações padrão podem já ter permissões amplas. Esse Role não revoga ClusterRoles existentes. O administrador do KEDA deve restringir custom resources no chart conforme a versão instalada.

ArgoCD deve ignorar somente spec.requestedReplicas nesta CR, preservando ownership dos limites, modo e segurança. No ECK, ignore apenas count do NodeSet autorizado e annotations operacionais. Use RespectIgnoreDifferences=true. O primeiro apply inicializa requestedReplicas; os syncs seguintes devem preservar o valor do HPA.

## Referências oficiais

- https://keda.sh/docs/2.21/concepts/scaling-deployments/
- https://keda.sh/docs/2.21/reference/scaledobject-spec/
- https://keda.sh/docs/2.21/scalers/cron/
- https://keda.sh/docs/2.21/scalers/prometheus/
- https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#scale-subresource

## Local runtime scenario

[Docker Compose laboratory](local-lab.md) provisions a disposable Kubernetes with
standard KEDA, Prometheus/cron scalers and real ECK/Elasticsearch. No custom KEDA
fork or external scaler is required. Source credentials belong to the KEDA
integration; Elasticsearch monitor credentials and Kubernetes permissions still
belong to this operator. See the laboratory guide for the full ownership table.
