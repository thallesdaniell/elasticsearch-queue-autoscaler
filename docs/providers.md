# Demanda KEDA e composição

KEDA é o único provider principal. O operador recebe requestedReplicas pelo /scale e não consulta APIs de filas/métricas diretamente. Pub/Sub, Datadog, Prometheus e outras fontes pertencem ao ScaledObject. Autenticação e qualidade da métrica ficam no KEDA. Veja [contrato](keda.md).

## Pisos por horário

Prefira triggers cron no ScaledObject. O HPA combina as recomendações por máximo. O operador preserva pisos locais opcionais em sources.schedules para quem precisa de uma política adicional independente do KEDA:

```yaml
sources:
  schedules:
    - name: business-hours
      timeZone: America/Recife
      days: [1, 2, 3, 4, 5]
      start: "08:00"
      end: "18:00"
      replicas: 5
```

Janelas locais são [start,end), dias 0=domingo a 6=sábado. Janelas noturnas pertencem ao dia inicial; horários iguais são inválidos. Em DST, horas repetidas são avaliadas nas duas ocorrências e horas inexistentes não ocorrem. A base tzdata está embutida no binário. Essas regras descrevem o engine local, não reimplementam a semântica cron do KEDA.

| Situação | Alvo antes de maxStep/cooldown |
|---|---:|
| Piso local 5, KEDA 8 | 8 |
| Pisos locais 5 e 8, KEDA 4 | 8 |
| Janela encerra, atual 8, KEDA 2 | 8 |
| Sem janela, atual 3, KEDA 7 | 7 |
| KEDA indisponível, pedido anterior 8 | Pedido 8 permanece, sujeito à segurança |

MaxReplicas limita novos aumentos, mas não reduz nós existentes acima do máximo. A demanda não expira automaticamente. Use Audit para interromper atuação. Janelas devem antecipar estabilização, cooldown e provisionamento. Não há previsão nem scale down.
