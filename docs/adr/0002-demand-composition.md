# ADR 0002: múltiplas fontes por máximo

Status: aceito para o MVP, 2026-10-11.

Contexto: o agendamento não pode reduzir um crescimento solicitado pelo KEDA. Sobreposições precisam de uma regra determinística.

Decisão: horários são pisos de réplicas. Usar o máximo entre minReplicas, requestedReplicas do KEDA e janelas ativas. Nunca somar capacidades de janelas sobrepostas. Aplicar maxReplicas e maxStep e manter current como limite inferior absoluto. A janela encerrada não causa downscale no MVP.

Semântica temporal: timezone IANA obrigatório; dias numéricos e HH:MM; início inclusivo/fim exclusivo; janelas noturnas pertencem ao dia inicial. Em DST, avaliar o relógio civil local: horas repetidas ativam ambas as ocorrências, horas inexistentes não ocorrem.

Falhas: a política original de falha fechada do Pub/Sub foi substituída pela ADR 0004. A demanda KEDA é persistida e não traz timestamp. O operador valida a segurança do ECK em cada ciclo, mas não infere freshness de métricas a partir de /scale.

Consequências: a capacidade retida aumenta monotonicamente e pode gerar custo até intervenção manual. A evolução de downscale deverá considerar todas as fontes ativas, nunca um provider isolado.
