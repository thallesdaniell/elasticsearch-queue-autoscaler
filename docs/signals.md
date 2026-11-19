# Contrato dos sinais migrados para KEDA

Queries e thresholds dependem de cada ambiente e não fazem parte deste repositório. Não inventar valores de produção a partir dos exemplos. Registrar dono, unidade, labels do NodeSet, janela, atraso de publicação, cardinalidade e teste de falha para cada sinal antes de Auto.

| Sinal legado | Semântica a confirmar | Migração |
|---|---|---|
| CPU | Percentual 0–100, média ou máximo só dos data nodes desejados | Scaler Datadog/Prometheus com Value sobre a utilização externa, ou CPU/Utilization dos Pods com requests corretos; não usar AverageValue como se percentual fosse backlog total |
| Fila do Elasticsearch | Máximo de fila por nó/thread pool, não tamanho de subscription | Query agregada por NodeSet e política calibrada; não assumir capacidade linear a partir de uma fila local |
| Rejeições | Taxa/incremento numa janela, não contador cumulativo | rate/increase ou equivalente; zero é válido, ausência é erro. Threshold estritamente positivo e normalização de quantidade solicitada precisam ser definidos |
| Latência SLO | Segundos ou ms, percentil/aggregation e rota | Converter explicitamente para unidade comum; não reutilizar 500 como segundos sem confirmar. Métrica normalizada com Value é diferente de backlog |
| Mensagem mais antiga | Idade em segundos, não número de mensagens | Calibrar resposta com limites; idade isolada pode subir por mensagem problemática sem benefício |
| Backlog total | Mensagens da fila que o serviço pode processar | AverageValue com capacidade por nó; exemplo Prometheus existente |
| Horário | Piso de capacidade por janela | Cron KEDA, mesma timezone e fronteiras; overlap por maior recomendação |

As fórmulas do HPA diferem: AverageValue divide uma métrica total pelo alvo por réplica; Value escala proporcionalmente à relação observado/alvo e à contagem corrente. Não configurar todos os sinais com o mesmo metricType. Se a fonte já fornece número de réplicas calculado, documentar a normalização exata; não passar esse número como CPU ou backlog.

Políticas legadas podem aumentar um ou mais degraus após sequências de observações. A política nativa KEDA/HPA com múltiplos triggers não reproduz automaticamente essa máquina de estados nem uma escada preexistente. Nosso motor tem maxStep em nós, cooldown e estabilização temporal. Para equivalência estrita, desenhar um scaler/adaptador de recomendação próprio e testá-lo; não há essa equivalência neste MVP.

Datadog é uma opção suportada pelo KEDA, não uma dependência do operador. Use TriggerAuthentication com Secrets/identidade conforme o scaler; nenhum segredo vai para Git. Para métricas ausentes, evitar metricUnavailableValue=0 ou fallback que mascare falha sem uma decisão explícita. /scale não permite ao operador distinguir demanda fresca de pedido antigo.

Teste de aceite: carga normal, CPU alta com latência normal, CPU baixa com latência alta, rejeições isoladas, fila noturna, mensagem sem progresso, série ausente/antiga/múltipla e dois triggers divergentes. Confirmar resposta KEDA/HPA e limites ECK com logs e métricas; a possibilidade técnica de um scaler não significa que a query de produção foi configurada.
