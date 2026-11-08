package gamestats

import scala.collection.concurrent.TrieMap
import ujson.*

private final case class StoredMatch(analysis: MatchAnalysis, patterns: List[DetectedPattern])

final class AnalysisService(
    analyzers: Map[String, GameAnalyzer] = GameAnalyzers.registry
):
  private val store = TrieMap.empty[(String, String), StoredMatch]

  def analyze(batch: Batch): Option[MatchAnalysis] =
    analyzers.get(batch.gameId).map { analyzer =>
      val result = analyzer.analyze(batch)
      store.update((batch.gameId, batch.matchId), StoredMatch(result, PatternDetectors.detect(batch)))
      result
    }

  def getMatches(matchId: String, gameId: Option[String] = None): List[MatchAnalysis] =
    store.iterator.collect {
      case ((storedGameId, storedMatchId), stored)
          if storedMatchId == matchId && gameId.forall(_ == storedGameId) => stored.analysis
    }.toList.sortBy(_.gameId)

  def getPatterns(
      gameId: Option[String] = None,
      patternType: Option[String] = None,
      matchId: Option[String] = None
  ): List[DetectedPattern] =
    store.iterator.flatMap { case ((storedGameId, storedMatchId), stored) =>
      stored.patterns.filter(pattern =>
        gameId.forall(_ == storedGameId) &&
          patternType.forall(_ == pattern.patternType) &&
          matchId.forall(_ == storedMatchId)
      )
    }.toList.sortBy(pattern => (pattern.gameId, pattern.matchId, pattern.patternType, pattern.playerId))

  def getPlayerStats(playerId: String): Option[Obj] =
    val entries = playerEntries(playerId)
    Option.when(entries.nonEmpty) {
      val games = entries.groupBy(_._1.analysis.gameId).toList.sortBy(_._1).map { case (gameId, matches) =>
        gameId -> aggregate(gameId, matches)
      }
      Obj("playerId" -> Str(playerId), "games" -> Obj.from(games))
    }

  def leaderboard(gameId: String): List[Obj] =
    val players = store.valuesIterator.toList.flatMap { stored =>
      if stored.analysis.gameId == gameId then
        stored.analysis.players.map(stats => stats.playerId -> (stored, stats))
      else Nil
    }.groupBy(_._1).toList.map { case (playerId, records) =>
      playerId -> aggregate(gameId, records.map(_._2))
    }
    val ranked = gameId match
      case "combat-arena" => players.sortBy { case (playerId, stats) =>
        (-stats("kills").num, -stats("kd").num, playerId)
      }
      case "racing-circuit" => players.sortBy { case (playerId, stats) =>
        val bestLap = stats("bestLapMs") match
          case Num(value) => value
          case _ => Double.PositiveInfinity
        (-stats("wins").num, bestLap, playerId)
      }
      case "connect-four" => players.sortBy { case (playerId, stats) =>
        (-stats("wins").num, stats("moves").num, playerId)
      }
      case "snake-arena" => players.sortBy { case (playerId, stats) =>
        (-stats("bestScore").num, playerId)
      }
      case "memory-match" => players.sortBy { case (playerId, stats) =>
        (-stats("pairs").num, stats("attempts").num, playerId)
      }
      case _ => Nil
    ranked.map { case (playerId, stats) =>
      Obj.from(Seq("playerId" -> Str(playerId)) ++ stats.value.toSeq)
    }

  private def playerEntries(playerId: String): List[(StoredMatch, PlayerStats)] =
    store.valuesIterator.toList.flatMap { stored =>
      stored.analysis.players.find(_.playerId == playerId).map(stored -> _)
    }

  private def aggregate(gameId: String, entries: List[(StoredMatch, PlayerStats)]): Obj = gameId match
    case "combat-arena" =>
      val players = entries.collect { case (_, stats: CombatPlayerStats) => stats }
      val kills = players.map(_.kills).sum
      val deaths = players.map(_.deaths).sum
      val shotsFired = players.map(_.shotsFired).sum
      val shotsHit = players.map(_.shotsHit).sum
      val damage = players.map(_.damageTotal).sum
      val headshots = players.map(_.headshotCount).sum
      Obj(
        "matches" -> Num(players.size),
        "kills" -> Num(kills),
        "deaths" -> Num(deaths),
        "kd" -> Num(kills.toDouble / math.max(deaths, 1)),
        "damage" -> Num(damage),
        "avgDamage" -> Num(damage / math.max(shotsHit, 1)),
        "headshotPct" -> Num(headshots.toDouble * 100 / math.max(shotsHit, 1)),
        "shotsFired" -> Num(shotsFired),
        "shotsHit" -> Num(shotsHit),
        "accuracy" -> Num(shotsHit.toDouble / math.max(shotsFired, 1))
      )
    case "racing-circuit" =>
      val players = entries.collect { case (stored, stats: RacingPlayerStats) => stored -> stats }
      val lapTimes = players.flatMap(_._2.lapTimes)
      val average = lapTimes match
        case Nil => None
        case values => Some(values.sum.toDouble / values.size)
      val stdDev = average.map(mean => math.sqrt(lapTimes.map(time => math.pow(time - mean, 2)).sum / lapTimes.size))
      val wins = players.count { case (stored, _) =>
        stored.analysis.complete && (stored.analysis.stats match
          case RacingMatchStats(Some(classification), _, _) => classificationWinner(classification).contains(entries.head._2.playerId)
          case _ => false)
      }
      Obj(
        "matches" -> Num(players.size),
        "wins" -> Num(wins),
        "bestLapMs" -> lapTimes.minOption.map(Num(_)).getOrElse(Null),
        "avgLapMs" -> average.map(Num(_)).getOrElse(Null),
        "stdDevLapMs" -> stdDev.map(Num(_)).getOrElse(Null),
        "positionsGained" -> Num(players.map(_._2.positionsGained).sum),
        "penalties" -> Num(players.map(_._2.penalties).sum),
        "penaltySeconds" -> Num(players.map(_._2.penaltySeconds).sum)
      )
    case "connect-four" =>
      val players = entries.collect { case (_, stats: ConnectFourPlayerStats) => stats }
      val wins = entries.count { case (stored, stats) =>
        stored.analysis.complete && (stored.analysis.stats match
          case ConnectFourMatchStats(_, _, _, Some(winnerId), _, _, _) => winnerId == stats.playerId
          case _ => false)
      }
      val losses = entries.count { case (stored, stats) =>
        stored.analysis.stats match
          case ConnectFourMatchStats(_, _, Some("LOSS"), winnerId, _, _, _) =>
            winnerId.forall(_ != stats.playerId)
          case _ => false
      }
      val draws = entries.count { case (stored, _) =>
        stored.analysis.stats match
          case ConnectFourMatchStats(_, _, Some("DRAW"), _, _, _, _) => true
          case _ => false
      }
      val decisions = players.map(_.aiDecisions).sum
      val totalTime = players.flatMap(stats =>
        stats.avgAiTimeMs.map(_ * stats.aiDecisions)
      ).sum
      Obj(
        "matches" -> Num(players.size),
        "wins" -> Num(wins),
        "losses" -> Num(losses),
        "draws" -> Num(draws),
        "moves" -> Num(players.map(_.moves).sum),
        "aiDecisions" -> Num(decisions),
        "avgAiTimeMs" -> (if decisions == 0 then Null else Num(totalTime / decisions)),
        "totalNodes" -> Num(players.map(_.totalNodes).sum)
      )
    case "snake-arena" =>
      val players = entries.collect { case (_, stats: SnakePlayerStats) => stats }
      val scores = players.map(_.score)
      Obj(
        "matches" -> Num(players.size),
        "wins" -> Num(entries.count { case (stored, _) =>
          stored.analysis.complete && (stored.analysis.stats match
            case SnakeMatchStats(_, Some("WIN"), _, _, _, _) => true
            case _ => false)
        }),
        "bestScore" -> Num(scores.maxOption.getOrElse(0)),
        "avgScore" -> Num(if scores.isEmpty then 0 else scores.sum.toDouble / scores.size),
        "longestLength" -> Num(players.map(_.length).maxOption.getOrElse(0)),
        "foodEaten" -> Num(players.map(_.foodEaten).sum)
      )
    case "memory-match" =>
      val players = entries.collect { case (_, stats: MemoryPlayerStats) => stats }
      val attempts = players.map(_.attempts).sum
      val pairs = players.map(_.pairs).sum
      Obj(
        "matches" -> Num(players.size),
        "wins" -> Num(entries.count { case (stored, _) =>
          stored.analysis.complete && (stored.analysis.stats match
            case MemoryMatchStats(_, Some("WIN"), _, _, _, _) => true
            case _ => false)
        }),
        "pairs" -> Num(pairs),
        "attempts" -> Num(attempts),
        "flips" -> Num(players.map(_.flips).sum),
        "accuracy" -> Num(if attempts == 0 then 0.0 else pairs.toDouble / attempts)
      )
    case _ => Obj()

  private def classificationWinner(classification: Value): Option[String] = classification match
    case Arr(values) => values.headOption.flatMap {
      case Str(playerId) => Some(playerId)
      case obj: Obj => obj.value.get("playerId").orElse(obj.value.get("id")).collect { case Str(id) => id }
      case _ => None
    }
    case _ => None