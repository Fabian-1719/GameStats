package gamestats

import ujson.*

/** Pruebas de los juegos de GameConsole con valores calculados a mano (sección 2.4 de la guía). */
class GameConsoleStatsSuite extends munit.FunSuite:
  private val start = java.time.Instant.parse("2026-11-16T12:00:00Z")

  private def event(
      id: String,
      eventType: String,
      playerId: Option[String],
      data: Value = Obj(),
      ms: Long = 0
  ): Event =
    Event(id, start.plusMillis(ms).toString, "test-game", "1.0", "m", playerId, eventType, None, data)

  // ---------- Cuatro en Línea ----------

  /** Humano gana con 4 jugadas propias contra la IA de nivel 3 (7 jugadas en total). */
  private def connectFourWin(matchId: String, humanMoves: Int, level: Int = 3): Batch =
    val moves = (1 to humanMoves).toList.flatMap { i =>
      val human = event(s"h$i", "MOVE", Some("p1"), Obj("column" -> Num(3)), i * 2000L)
      if i < humanMoves then
        List(
          human,
          event(s"d$i", "AI_DECISION", Some("cpu-prolog"),
            Obj("level" -> Num(level), "nodes" -> Num(100), "timeMs" -> Num(10 * i)), i * 2000L + 500),
          event(s"c$i", "MOVE", Some("cpu-prolog"), Obj("column" -> Num(2)), i * 2000L + 1000)
        )
      else List(human)
    }
    Batch(matchId, "connect-four", "COMPLETE",
      (event("s", "MATCH_STARTED", None, Obj("players" -> Arr(Str("p1"), Str("cpu-prolog")))) :: moves) :+
        event("f", "MATCH_FINISHED", None,
          Obj("winnerId" -> Str("p1"), "result" -> Str("WIN"), "durationMs" -> Num(humanMoves * 2000L)),
          humanMoves * 2000L)
    )

  test("Cuatro en Línea: nivel de la IA y tiempo medio de decisión") {
    // Decisiones de 10, 20 y 30 ms -> promedio (10+20+30)/3 = 20 ms
    val result = new ConnectFourAnalyzer().analyze(connectFourWin("c4-a", humanMoves = 4))
    val stats = result.stats.asInstanceOf[ConnectFourMatchStats]
    assertEquals(stats.aiLevel, Some(3))
    assertEquals(stats.avgAiTimeMs, Some(20.0))
    assertEquals(stats.aiDecisions, 3)
    assertEquals(stats.moves, 7) // 4 del humano + 3 de la IA
    assertEquals(stats.winnerId, Some("p1"))
  }

  test("Victoria relámpago: se detecta con 10 jugadas o menos y no con 11") {
    val fast = PatternDetectors.detect(connectFourWin("c4-fast", humanMoves = 10))
    assertEquals(fast.map(_.patternType), List("LIGHTNING_WIN"))
    assertEquals(fast.head.playerId, "p1")
    assertEquals(fast.head.detail("moves").num, 10.0)

    val slow = PatternDetectors.detect(connectFourWin("c4-slow", humanMoves = 11))
    assertEquals(slow, Nil)
  }

  test("Cuatro en Línea: % de victorias del jugador contra la IA") {
    // 2 partidas ganadas y 1 perdida -> 2/3
    val service = new AnalysisService()
    service.analyze(connectFourWin("c4-1", 4))
    service.analyze(connectFourWin("c4-2", 5))
    val loss = Batch("c4-3", "connect-four", "COMPLETE", List(
      event("s", "MATCH_STARTED", None, Obj("players" -> Arr(Str("p1"), Str("cpu-prolog")))),
      event("h", "MOVE", Some("p1"), ms = 1000),
      event("c", "MOVE", Some("cpu-prolog"), ms = 2000),
      event("f", "MATCH_FINISHED", None, Obj("winnerId" -> Str("cpu-prolog"), "result" -> Str("LOSS")), 3000)
    ))
    service.analyze(loss)

    val p1 = service.getPlayerStats("p1").get("games")("connect-four")
    assertEquals(p1("matches").num, 3.0)
    assertEquals(p1("wins").num, 2.0)
    assertEquals(p1("losses").num, 1.0)
    assertEqualsDouble(p1("winRate").num, 2.0 / 3, 1e-9)
  }

  // ---------- Snake ----------

  private def snake(matchId: String, foodTimesMs: List[Long], durationMs: Long, score: Int): Batch =
    val food = foodTimesMs.zipWithIndex.map { case (ms, i) =>
      event(s"food$i", "FOOD_EATEN", Some("p2"), Obj("score" -> Num((i + 1) * 10), "length" -> Num(4 + i)), ms)
    }
    Batch(matchId, "snake-arena", "COMPLETE",
      event("s", "MATCH_STARTED", None, Obj("players" -> Arr(Str("p2")))) ::
        food ++ List(
          event("d", "PLAYER_DIED", Some("p2"), Obj("cause" -> Str("WALL"), "score" -> Num(score)), durationMs),
          event("f", "MATCH_FINISHED", None,
            Obj("result" -> Str("LOSS"), "score" -> Num(score), "durationMs" -> Num(durationMs)), durationMs)
        )
    )

  test("Snake: comidas por minuto") {
    // 6 comidas en 2 minutos (120 000 ms) -> 3 comidas por minuto
    val batch = snake("sn-1", List(10_000L, 20_000, 30_000, 40_000, 50_000, 60_000), 120_000L, 60)
    val stats = new SnakeArenaAnalyzer().analyze(batch).stats.asInstanceOf[SnakeMatchStats]
    assertEquals(stats.foodEaten, 6)
    assertEquals(stats.durationMs, Some(120_000L))
    assertEqualsDouble(stats.foodPerMinute.get, 3.0, 1e-9)
  }

  test("Racha de Snake: 5 comidas en menos de 15 s") {
    // Comidas en 0, 3, 6, 9 y 12 s -> las 5 caben en 12 s
    val streak = snake("sn-2", List(1_000L, 4_000, 7_000, 10_000, 13_000), 30_000L, 50)
    val found = PatternDetectors.detect(streak)
    assertEquals(found.map(_.patternType), List("FOOD_STREAK"))
    assertEquals(found.head.detail("count").num, 5.0)

    // Comidas cada 4 s -> la 1.a y la 5.a quedan a 16 s: no es racha
    val noStreak = snake("sn-3", List(1_000L, 5_000, 9_000, 13_000, 17_000), 30_000L, 50)
    assertEquals(PatternDetectors.detect(noStreak), Nil)
  }

  test("Snake: puntaje máximo, promedio y comidas por minuto del jugador") {
    // Puntajes 30 y 50 -> máx 50, promedio 40. Ritmos 3/min y 1/min -> promedio 2/min
    val service = new AnalysisService()
    service.analyze(snake("sn-a", List(10_000L, 20_000, 30_000), 60_000L, 30))
    service.analyze(snake("sn-b", List(10_000L, 20_000, 30_000, 40_000, 50_000), 300_000L, 50))
    val p2 = service.getPlayerStats("p2").get("games")("snake-arena")
    assertEquals(p2("bestScore").num, 50.0)
    assertEquals(p2("avgScore").num, 40.0)
    assertEqualsDouble(p2("avgFoodPerMinute").num, 2.0, 1e-9)
  }

  // ---------- Memoria ----------

  private def memory(matchId: String, matched: Int, missed: Int, result: String = "WIN"): Batch =
    val hits = (1 to matched).toList.map(i =>
      event(s"m$i", "PAIR_MATCHED", Some("p3"), Obj("pairId" -> Str(s"pair-$i"), "attempts" -> Num(i)), i * 1000L)
    )
    val misses = (1 to missed).toList.map(i =>
      event(s"x$i", "PAIR_MISSED", Some("p3"), Obj("attempts" -> Num(matched + i)), (matched + i) * 1000L)
    )
    val end = (matched + missed + 1) * 1000L
    Batch(matchId, "memory-match", "COMPLETE",
      (event("s", "MATCH_STARTED", None, Obj("players" -> Arr(Str("p3")))) :: (hits ++ misses)) :+
        event("f", "MATCH_FINISHED", None,
          Obj("winnerId" -> Str("p3"), "result" -> Str(result), "pairs" -> Num(matched),
            "attempts" -> Num(matched + missed)), end)
    )

  test("Memoria: precisión = PAIR_MATCHED / (MATCHED + MISSED)") {
    // 6 aciertos y 2 fallos -> 6 / 8 = 0.75
    val stats = new MemoryMatchAnalyzer().analyze(memory("mm-1", 6, 2)).stats.asInstanceOf[MemoryMatchStats]
    assertEquals(stats.pairs, 6)
    assertEquals(stats.attempts, 8)
    assertEqualsDouble(stats.accuracy, 0.75, 1e-9)
  }

  test("Memoria perfecta: solo cuando se gana sin ningún PAIR_MISSED") {
    val perfect = PatternDetectors.detect(memory("mm-2", 6, 0))
    assertEquals(perfect.map(_.patternType), List("PERFECT_MEMORY"))
    assertEquals(perfect.head.playerId, "p3")
    assertEquals(PatternDetectors.detect(memory("mm-3", 6, 1)), Nil)
    assertEquals(PatternDetectors.detect(memory("mm-4", 6, 0, result = "ABANDONED")), Nil)
  }

  test("Memoria: mejor número de intentos entre las partidas ganadas") {
    // Ganadas con 9 y 7 intentos; la abandonada (3 intentos) no cuenta -> mejor 7
    val service = new AnalysisService()
    service.analyze(memory("mm-a", 6, 3))
    service.analyze(memory("mm-b", 6, 1))
    service.analyze(memory("mm-c", 2, 1, result = "ABANDONED"))
    val p3 = service.getPlayerStats("p3").get("games")("memory-match")
    assertEquals(p3("wins").num, 2.0)
    assertEquals(p3("bestAttempts").num, 7.0)
  }
