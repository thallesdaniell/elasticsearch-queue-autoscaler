# Especificação: autoscaling ECK orientado por KEDA

Estado: contrato do MVP e critérios de adoção, revisão 2026-10-12. A implementação usa API alpha. Este documento registra as decisões de arquitetura, os requisitos verificáveis e os limites conhecidos. Não certifica comportamento de produção nem substitui aprovação operacional de uma migração.

## 1. Problema e justificativa

Há dois tipos de decisão válidos: capacidade antecipada por horário e capacidade reativa por pressão da aplicação. O problema não é a existência dessas duas políticas, mas escritores independentes atuando no mesmo Elasticsearch sem contrato único de ownership, progresso e recuperação. Implementações legadas independentes podem produzir decisões concorrentes e estados difíceis de recuperar.

A configuração versionada é apenas um insumo a reconciliar com o ambiente. Copiar números de exemplos ou presumir equivalência entre configuração declarada e capacidade aplicada não é uma migração segura.

KEDA é o provider principal. A solução terá um único executor lógico de patches ECK por namespace, replicado com leader election, com políticas explícitas por NodeSet. KEDA/HPA será dono somente da demanda. ECK permanece responsável por Pods, StatefulSets, volumes e alocação de shards.

Estender um executor existente pode reduzir mudanças no curto prazo, especialmente se downscale e réplicas de índices forem obrigatórios imediatamente. O novo operador se justifica por API declarativa, integração KEDA, segurança testável e distribuição reutilizável. Ele não deve ser adotado como substituto integral antes de atender os gates deste contrato.

## 2. Responsabilidades e ownership

| Componente | Responsabilidade | Não possui |
|---|---|---|
| ScaledObject/KEDA/HPA | Métricas, cron, autenticação da origem, requestedReplicas via /scale | spec.nodeSets, configuração de índices |
| ElasticsearchQueueAutoscaler | Limites, passos, estabilização, auditoria, segurança, patch autorizado | StatefulSets, PVCs, réplicas de índices |
| ECK | Orquestração do Elasticsearch e reconciliação dos nós | Política de demanda externa |
| GitOps/administrador | Alvos autorizados, política, limites, manutenção e imagem | Valores dinâmicos delegados durante sync |
| Autoscaler de infraestrutura | Oferta de nós/VMs conforme Pods e configuração própria | Demanda de Elasticsearch |

Uma CR e um ScaledObject por NodeSet. Um Elasticsearch pode autorizar vários NodeSets por `queue-autoscaler.io/owners`, um mapa JSON de NodeSet para nome de CR. A autorização antiga owner/node-set é aceita se o mapa não existir. Mapa inválido bloqueia, sem fallback. Todas as CRs ficam no mesmo namespace do Elasticsearch.

As réplicas do operador compartilham uma Lease namespaced e executam uma reconciliação por vez. Para um Elasticsearch, nenhuma nova alteração é iniciada enquanto o cluster não convergir. A annotation pending-node-set identifica a qual NodeSet pertence pending-count; resourceVersion protege mudanças entre leitura e patch. O cooldown é por Elasticsearch e pode ser conservador para o segundo NodeSet. Não há garantia de fairness/prioridade entre NodeSets nesta versão.

## 3. Requisitos verificáveis

| ID | Requisito obrigatório | Implementação / evidência |
|---|---|---|
| DEM-01 | KEDA escreve somente requestedReplicas via /scale | CRD gerada e envtest leitura/update/conflito |
| DEM-02 | Maior demanda vence, com limite global e passo | engine.Desired e testes KEDA/piso |
| DEM-03 | Sobreposição local independente da ordem; fuso IANA | ScheduleFloor, testes de fronteira, madrugada e DST |
| DEM-04 | Nenhum pedido baixo, zero ou max reduzido pode diminuir count | engine, ScalePatch e testes |
| OWN-01 | Apenas NodeSets explicitamente autorizados podem mudar | Authorized, Target, testes de mapa e isolamento |
| OWN-02 | Dois NodeSets do mesmo CR não escalam em paralelo | pending-node-set + estabilidade global; fake client e envtest |
| OWN-03 | Mudanças concorrentes invalidam o patch | test resourceVersion/nome/count; envtest CAS |
| SAFE-01 | ECK observou generation; todos os Pods convergiram | stable + leitura direta, teste de restart |
| SAFE-02 | Saúde, shards, disco e contagem ES verificáveis | safety.Check, testes TLS simulados |
| SAFE-03 | Operação parada gera diagnóstico sem rollback destrutivo | convergenceTimeoutSeconds, PendingSince, ScaleStalled, teste de restart |
| SAFE-04 | Hosts distintos quando a política exigir | requireDistinctNodes opt-in e teste |
| OPS-01 | Audit e DryRun não persistem count | defaults e envtest DryRun |
| OPS-02 | Estado de recuperação sobrevive falha após patch | count, last-scale, pending-count, pending-node-set e cooldown-until atômicos |
| OPS-03 | Atualização HPA não adia estabilização indefinidamente | hash de política exclui pedido, testes de demanda e política |
| OBS-01 | Distinguir pedido, count aplicado, Pods e prontos | status e eqa_replicas por kind |
| OBS-02 | Expor ausência de decisões e espera prolongada | last_reconcile_timestamp_seconds e pending_seconds; exemplos de alertas |
| GIT-01 | GitOps preserva apenas os campos delegados | exemplo ArgoCD com RespectIgnoreDifferences |
| SEC-01 | Sem chave cloud no operador; TLS ES e API key monitor | main, client-go, safety, RBAC namespaced |

## 4. Contrato de demanda e falhas

`requestedReplicas` é intenção persistida, não uma amostra de métrica. `/scale` não traz idade, TTL, heartbeat nem identificação do trigger que motivou o pedido. O operador não consegue reconstruir CPU, SLO ou freshness só pelo count. KEDA/HPA pode aumentar por um trigger válido quando outro falha. Isso não equivale à regra antiga de bloquear tudo se qualquer fonte falhar.

Para pausar novos patches, usar Audit em todas as CRs afetadas. Pausar/remover ScaledObject sozinho deixa a demanda anterior. Antes de voltar para Auto, revisar pedido, métricas, ownership e capacidade. Overrides manuais não podem competir com HPA: pausar ScaledObject, manter Audit, registrar motivo/autor/validade, ajustar pedido e só então liberar Auto conforme procedimento. Mesmo override não habilita downscale ou ignora segurança.

A recomendação é max(min, pedido KEDA, pisos locais), limitada por maxReplicas e maxStep, sem ficar abaixo do count atual. Demanda fora de uma antiga escada de tiers é aceita como contagem contínua; não há equivalência automática com degraus 2/5/10/etc. nem salto de dois tiers por duas leituras. Se a organização exigir essa política exata, ela é um requisito adicional, não um parâmetro implícito do KEDA.

A matriz de sinais em signals.md define a transposição de CPU, fila, rejeições, latência e idade. Valores de threshold usados em exemplos são hipóteses de calibração, não defaults de produção. Filas por nó, backlog total e segundos não são intercambiáveis.

## 5. Segurança, capacidade e saúde degradada

Green é o padrão; yellow é opt-in explícito, ainda sujeito às demais validações. Red, Pods não prontos, phase diferente de Ready, shards em movimento ou limites excedidos bloqueiam. Isso mantém uma limitação: uma falha de capacidade em um NodeSet pode bloquear outro. Aceitar scale up durante degradação exige uma política de recuperação específica e permanece fora do MVP.

convergenceTimeoutSeconds gera ScaleStalled após espera contínua; não reduz count, não libera operação concorrente, não apaga marcadores e não garante provisionamento. O relógio persiste no status a partir da primeira reconciliação que observa o bloqueio; não é um detector de 15 minutos sem qualquer progresso. Reiniciar o Pod não apaga o relógio. Convergência limpa a espera. Um rollback que remove nós já provisionados é downscale, mesmo chamado de cancelamento.

requireDistinctNodes valida hosts Kubernetes do NodeSet, não zonas nem allocation awareness do Elasticsearch. Anti-affinity, topology spread, limites do node pool, StorageClass, quota, capacidade on-demand/spot e fallback são responsabilidade da infraestrutura. maxReplicas não comprova capacidade física disponível.

## 6. Gate específico de percolate

**Não declarar substituição funcional de um autoscaler legado apenas porque o count aumentou.** Algumas estratégias também alteram `number_of_replicas` associado à capacidade. Nosso MVP não faz essa escrita. Se os novos nós não receberem shards úteis do índice alvo, aumentar count pode apenas aumentar custo.

Antes de Auto, inventariar primaries/replicas, filtros de alocação, tiers, zonas, routing, tamanho de shards e Pods/hosts. Medir distribuição e throughput/SLO antes/depois com carga representativa. Não assumir que replicas=nodes-1 é requisito universal ou que mudar spare resolve segurança em toda topologia. Se houver dependência real de modificar réplicas, manter o executor existente nesse alvo até uma estratégia de índice revisada ou extensão com workflow persistente e testes de falha. Não coexistir dois writers do count.

## 7. Fora do MVP, com impacto explícito

- Scale down: adiado. A economia noturna dos sistemas atuais NÃO é preservada. Custo pode permanecer no pico até intervenção manual.
- Mutação/rollback de réplicas de índices: adiado. Exige snapshots por índice, tratamento de alias/wildcard, falha parcial e restart.
- Up em red/Pending ou operação simultânea de dois NodeSets: não autorizado. Exige nova política de risco.
- Escadas exatas, jumps de emergência e teto consultado diretamente no cloud provider: não implementados.
- Validade forte da demanda, backpressure do worker, circuit breaker de 429, separar base on-demand/burst spot e cluster dedicado: dependências/evoluções, não resolvidas por este controller.
- ONCE e DogStatsD: não portados. Reconciliação contínua e Prometheus substituem o mecanismo, não os alertas existentes automaticamente.

## 8. Critérios de aceite para corte

1. Inventário vivo reconciliado com Git/monitoramento: nomes reais de CR/NodeSets, contagens, índices, sinais, limites e writers. A validação exige acesso separado ao estado real do ambiente.
2. Métricas com unidade, escopo por NodeSet, cardinalidade, timestamps e comportamento em ausência definidos; ScaledObject/HPA saudáveis e um por alvo.
3. Aprovação explícita de custo sem downscale e do gate de distribuição/throughput de percolate.
4. Audit e DryRun comparados ao executor atual, horários úteis/sábado/domingo e pressão fora da janela testados.
5. Teste real KEDA/HPA/ECK/Elasticsearch: autorização, conflito, dois NodeSets, restart, scale pendente, timeout sem rollback, Pods distintos quando exigido, ArgoCD e credenciais.
6. Corte por Elasticsearch: suspender todos os writers concorrentes e esperar Jobs ativos; retirar só as entradas desse alvo do schedule central. Colocar o novo executor em Auto após confirmar o término.
7. Rollback operacional começa em Audit, espera operação ECK pendente e transfere ownership de maneira controlada. Não reativar cegamente um writer antigo com downscale/replica policy desatualizados.
8. Registrar latência, throughput, filas, rejeições, disponibilidade, recuperação e custo. Nenhum gate de produção foi executado neste repositório.

## 9. Rastreabilidade das decisões

Os ADRs e testes do repositório são a fonte de verdade para o comportamento implementado.

O cooldown-until gravado pelo writer anterior é respeitado também pelos demais NodeSets, mesmo que tenham cooldownSeconds menor. Para novos patches vale ainda o cooldown da política que está sendo avaliada. Ambos são persistidos/avaliados com o relógio UTC.
