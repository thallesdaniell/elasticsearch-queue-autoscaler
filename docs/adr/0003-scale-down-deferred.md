# ADR 0003: scale down fora do MVP

Status: aceito para o MVP, 2026-10-11.

A API exige blockScaleDown=true e o motor impede qualquer retorno inferior ao count atual. A função de patch também recusa desired<=current. Não há flag secreta para habilitar redução.

Para adicionar scale down: definir capacidade restante de disco/CPU/heap, limites de shards, allocation awareness, réplicas, tiers/ILM, tolerância a falhas e capacidade por zona. Exigir estabilidade prolongada, histerese, decremento de um nó e acompanhamento completo de evacuação via ECK. Planejar cancelamento/pausa, falhas de realocação, métricas ausentes, janelas futuras e testes de recuperação. Uma simulação bem-sucedida não substitui testes de falha com dados descartáveis e restauração validada.
