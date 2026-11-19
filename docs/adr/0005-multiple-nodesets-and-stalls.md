# ADR 0005: dois NodeSets, uma operação ECK de cada vez

Status: aceito em 2026-10-11. Evolui a restrição inicial da ADR 0001.

Contexto: os legados administram políticas distintas em dois NodeSets do mesmo Elasticsearch. Um único NodeSet autorizado não permite consolidar os writers. Permitir dois patches durante realocação contraria o requisito de aguardar convergência.

Decisão: uma CR/ScaledObject por NodeSet, mapa de autorização queue-autoscaler.io/owners no Elasticsearch e Lease única namespaced do manager. Owner legado é aceito somente na ausência do mapa. Um patch grava count, last-scale, pending-count, pending-node-set e cooldown-until no mesmo CAS. Todos os reconciles verificam o marcador no NodeSet ao qual ele pertence, a geração do ECK, Pods de todos os NodeSets e saúde ES antes de iniciar outro aumento.

Cooldown permanece por Elasticsearch; namespace tem reconciliação sequencial. Não implementamos justiça/prioridades de fila. Um alvo degradado pode bloquear os demais; essa limitação é deliberada até existir uma política de recuperação específica.

Timeout: status.pendingSince registra o primeiro bloqueio observado; é persistido e limpo após convergência. Depois de convergenceTimeoutSeconds (padrão 45min) o motivo muda para ScaleStalled. Não há rollback de count nem redução de réplicas. Assim não removemos nós que já entraram no cluster sob o nome de cancelamento.

Topologia: requireDistinctNodes opcional verifica que os Pods do NodeSet estão em hosts Kubernetes diferentes. Não configura agendamento nem substitui allocation awareness ou múltiplas zonas.

Observabilidade: expor demanda, count configurado, Pods observados/prontos, heartbeat e duração de espera. Preservar métricas estáveis sem rótulos de erro livre.

O cooldown-until gravado pelo writer anterior é respeitado também pelos demais NodeSets, mesmo que tenham cooldownSeconds menor. Para novos patches vale ainda o cooldown da política que está sendo avaliada. Ambos são persistidos/avaliados com o relógio UTC.
