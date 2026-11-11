package gamestats

import ujson.*

/** Pruebas de los datos que devuelven GET /v1/stats/general y GET /v1/games/{gameId}/stats. */
class StatsEndpointsSuite extends munit.FunSuite:
  private val start = java.time.Instant.parse("2026-11-17T12:00:00Z")

  private def event(eventType: String, playerId: Option[String], data: Value = Obj(), ms: Long = 0): Event =
    Event(java.util.UUID.randomUUID().toString, start.plusMillis(ms).toString, "g", "1.0", "m", playerId,
      eventType, None, data)

  /** Partida de Cuatro en Línea: 2 jugadas por lado y una decisión de la IA. 6 eventos. */
  private def connectFour(id: String, result: String, level: Int, aiTimeMs: Int, durationMs: Long): Batch =
    val winner = result match
      case "WIN" => Str("p1")
      case "LOSS" => Str("cpu-prolog")
      case _ => Null
    Batch(id, "connect-four", "COMPLETE", List(
      event("MATCH_STARTED", None, Obj("players" -> Arr(Str("p1"), Str("cpu-prolog")))),
      event("MOVE", Some("p1"), ms = 100),
      event("AI_DECISION", Some("cpu-prolog"), Obj("level" -> Num(level), "nodes" -> Num(50), "timeMs" -> Num(aiTimeMs)), 200),
      event("MOVE", Some("cpu-prolog"), ms = 300),
      event("MOVE", Some("p1"), ms = 400),
      event("MATCH_FINISHED", None, Obj("winnerId" -> winner, "result" -> Str(result)), durationMs)
    ))

  /** Partida de Snake de un jugador. 2 + comidas eventos. */
  private def snake(id: String, player: String, score: Int, food: Int, durationMs: Long, result: String = "LOSS"): Batch =
    Batch(id, "snake-arena", "COMPLETE",
      (event("MATCH_STARTED", None, Obj("players" -> Arr(Str(player)))) ::
        (1 to food).toList.map(i => event("FOOD_EATEN", Some(player), Obj("score" -> Num(i * 10), "length" -> Num(3 + i)), i * 1000L))) :+
        event("MATCH_FINISHED", None, Obj("result" -> Str(result), "score" -> Num(score)), durationMs)
    )

  test("stats/general: totales, partidas por juego, jugadores y eventos") {
    val service = new AnalysisService()
    service.analyze(connectFour("c1", "WIN", 2, 10, 60_000))   // 6 eventos
    service.analyze(connectFour("c2", "LOSS", 3, 30, 30_000))  // 6 eventos
    service.analyze(snake("s1", "p2", 30, 3, 60_000))          // 5 eventos
    service.analyze(snake("s2", "p1", 0, 0, 5_000, "ABANDONED")) // 2 eventos

    val general = service.generalStats()
    assertEquals(general("totalMatches").num, 4.0)
    assertEquals(general("completedMatches").num, 4.0)
    assertEquals(general("abandonedMatches").num, 1.0)
    assertEquals(general("matchesByGame")("connect-four").num, 2.0)
    assertEquals(general("matchesByGame")("snake-arena").num, 2.0)
    assertEquals(general("matchesByGame")("memory-match").num, 0.0) // aparece aunque no se haya jugado
    assertEquals(general("players").num, 3.0) // p1, p2 y cpu-prolog
    assertEquals(general("events").num, 19.0) // 6 + 6 + 5 + 2
  }

  test("stats/general: con GameStats recién arrancado todo está en cero") {
    val general = new AnalysisService().generalStats()
    assertEquals(general("totalMatches").num, 0.0)
    assertEquals(general("events").num, 0.0)
  }

  test("games/connect-four/stats: instancias, resultados, duración y nivel contra resultado") {
    val service = new AnalysisService()
    service.analyze(connectFour("c1", "WIN", 2, 10, 60_000))
    service.analyze(connectFour("c2", "LOSS", 3, 30, 30_000))
    service.analyze(connectFour("c3", "LOSS", 3, 50, 30_000))

    val stats = service.gameStats("connect-four").get
    assertEquals(stats("instances").num, 3.0)
    assertEquals(stats("wins").num, 1.0)
    assertEquals(stats("losses").num, 2.0)
    assertEquals(stats("avgDurationMs").num, 40_000.0) // (60 + 30 + 30) / 3 segundos

    val metrics = stats("metrics")
    assertEquals(metrics("avgAiTimeMs").num, 30.0) // (10 + 30 + 50) / 3
    val levels = metrics("byLevel").arr.toList
    assertEquals(levels.map(_("level").num), List(2.0, 3.0))
    assertEquals(levels(0)("humanWins").num, 1.0)
    assertEquals(levels(1)("matches").num, 2.0)
    assertEquals(levels(1)("aiWins").num, 2.0)
    assertEquals(levels(1)("avgAiTimeMs").num, 40.0) // (30 + 50) / 2
  }

  test("games/snake-arena/stats: métricas propias y conteo de patrones") {
    val service = new AnalysisService()
    service.analyze(snake("s1", "p2", 30, 3, 60_000))  // 3 comidas en 1 min -> 3/min
    service.analyze(snake("s2", "p3", 50, 5, 300_000)) // 5 comidas en 5 min -> 1/min; 5 comidas en 4 s = racha

    val stats = service.gameStats("snake-arena").get
    assertEquals(stats("instances").num, 2.0)
    assertEquals(stats("players").num, 2.0)
    assertEquals(stats("patterns")("FOOD_STREAK").num, 1.0)
    assertEquals(stats("metrics")("bestScore").num, 50.0)
    assertEquals(stats("metrics")("avgScore").num, 40.0)
    assertEquals(stats("metrics")("totalFoodEaten").num, 8.0)
    assertEqualsDouble(stats("metrics")("avgFoodPerMinute").num, 2.0, 1e-9)
  }

  test("games/{gameId}/stats: juego conocido sin partidas da ceros; juego desconocido da None (404)") {
    val service = new AnalysisService()
    val empty = service.gameStats("memory-match").get
    assertEquals(empty("instances").num, 0.0)
    assertEquals(empty("avgDurationMs"), Null)
    assertEquals(service.gameStats("tetris"), None)
  }
