package gamestats

import java.time.Instant
import scala.util.Try
import ujson.*

trait GameAnalyzer:
  def analyze(batch: Batch): MatchAnalysis


object AnalyzerSupport:
  def int(data: Value, key: String): Option[Int] = number(data, key).map(_.toInt)
  def long(data: Value, key: String): Option[Long] = number(data, key).map(_.toLong)
  def double(data: Value, key: String): Option[Double] = number(data, key)
  def string(data: Value, key: String): Option[String] = data.objOpt.flatMap(_.get(key)).collect { case Str(s) => s }
  def number(data: Value, key: String): Option[Double] =
    data.objOpt.flatMap(_.get(key)).collect { case Num(n) => n }
  def strings(data: Value, key: String): List[String] =
    data.objOpt.flatMap(_.get(key)).flatMap(_.arrOpt)
      .toList.flatten.collect { case Str(value) => value }

  def durationMs(batch: Batch): Option[Long] =
    val started = batch.events.filter(_.eventType == "MATCH_STARTED").flatMap(timestamp).minOption
    val endpoint =
      val finishes = batch.events.filter(_.eventType == "MATCH_FINISHED").flatMap(timestamp)
      if batch.status == "COMPLETE" then finishes.maxOption
      else batch.events.flatMap(timestamp).maxOption
    for
      start <- started
      end <- endpoint
    yield math.max(0L, end.toEpochMilli - start.toEpochMilli)

  private def timestamp(event: Event): Option[Instant] = Try(Instant.parse(event.timestamp)).toOption

final class CombatArenaAnalyzer extends GameAnalyzer:
  import AnalyzerSupport.*

  def analyze(batch: Batch): MatchAnalysis =
    val hits = batch.events.filter(_.eventType == "SHOT_HIT")
    val eliminations = batch.events.filter(_.eventType == "PLAYER_ELIMINATED")
    val fired = batch.events.filter(_.eventType == "SHOT_FIRED")
    val playerIds = batch.events.flatMap(event =>
      event.playerId.toList ++ (if event.eventType == "PLAYER_ELIMINATED" then string(event.data, "targetId").toList else Nil)
    ).distinct.sorted

    val players = playerIds.map { playerId =>
      val playerHits = hits.filter(_.playerId.contains(playerId))
      val shotsFired = fired.count(_.playerId.contains(playerId))
      val shotsHit = playerHits.size
      val kills = eliminations.count(_.playerId.contains(playerId))
      val deaths = eliminations.count(event => string(event.data, "targetId").contains(playerId))
      val damage = playerHits.flatMap(event => double(event.data, "damage")).sum
      val headshots = playerHits.count(event => event.data.objOpt.flatMap(_.get("headshot")).contains(Bool(true)))
      CombatPlayerStats(
        playerId = playerId,
        kills = kills,
        deaths = deaths,
        kd = kills.toDouble / math.max(deaths, 1),
        avgDamage = damage / math.max(shotsHit, 1),
        headshotPct = headshots.toDouble * 100 / math.max(shotsHit, 1),
        shotsFired = shotsFired,
        shotsHit = shotsHit,
        accuracy = shotsHit.toDouble / math.max(shotsFired, 1),
        damageTotal = damage,
        headshotCount = headshots
      )
    }
    val damageByPlayer = playerIds.map(id => id -> hits.filter(_.playerId.contains(id)).flatMap(e => double(e.data, "damage")).sum).toMap
    val killsByPlayer = playerIds.map(id => id -> eliminations.count(_.playerId.contains(id))).toMap
    val ranking = playerIds.map(id => RankingEntry(id, killsByPlayer(id), damageByPlayer(id)))
      .sortBy(entry => (-entry.kills, -entry.damage, entry.playerId))
    val finished = batch.events.find(_.eventType == "MATCH_FINISHED")
    val winner = if batch.status == "COMPLETE" then finished.flatMap(event => string(event.data, "winnerId")) else None
    MatchAnalysis(
      matchId = batch.matchId,
      gameId = batch.gameId,
      status = batch.status,
      complete = batch.status == "COMPLETE",
      players = players,
      stats = CombatMatchStats(
        durationMs = durationMs(batch),
        totalDamage = hits.flatMap(event => double(event.data, "damage")).sum,
        ranking = ranking,
        mvp = ranking.headOption.map(_.playerId),
        winnerId = winner
      )
    )

final class RacingCircuitAnalyzer extends GameAnalyzer:
  import AnalyzerSupport.*

  def analyze(batch: Batch): MatchAnalysis =
    val laps = batch.events.filter(_.eventType == "LAP_COMPLETED")
    val positionChanges = batch.events.filter(_.eventType == "POSITION_CHANGED")
    val penalties = batch.events.filter(_.eventType == "PENALTY")
    val playerIds = batch.events.flatMap(_.playerId).distinct.sorted
    val players = playerIds.map { playerId =>
      val times = laps.filter(_.playerId.contains(playerId)).flatMap(event => long(event.data, "lapTimeMs"))
      val mean = times match
        case Nil => None
        case values => Some(values.sum.toDouble / values.size)
      val stdDev = mean.map(avg => math.sqrt(times.map(time => math.pow(time - avg, 2)).sum / times.size))
      val playerChanges = positionChanges.filter(_.playerId.contains(playerId))
      val playerPenalties = penalties.filter(_.playerId.contains(playerId))
      RacingPlayerStats(
        playerId = playerId,
        bestLapMs = times.minOption,
        avgLapMs = mean,
        stdDevLapMs = stdDev,
        positionsGained = playerChanges.flatMap(event =>
          for from <- int(event.data, "from"); to <- int(event.data, "to") yield from - to
        ).sum,
        penalties = playerPenalties.size,
        penaltySeconds = playerPenalties.flatMap(event => double(event.data, "seconds")).sum,
        lapTimes = times
      )
    }
    val fastestLap = laps.flatMap(event =>
      for playerId <- event.playerId; lapTime <- long(event.data, "lapTimeMs") yield FastestLap(playerId, lapTime)
    ).sortBy(lap => (lap.lapTimeMs, lap.playerId)).headOption
    val finished = batch.events.find(_.eventType == "MATCH_FINISHED")
    val classification =
      if batch.status == "COMPLETE" then finished.flatMap(event => event.data.objOpt.flatMap(_.get("classification")))
      else None
    val overtakes = positionChanges.count(event =>
      (for from <- int(event.data, "from"); to <- int(event.data, "to") yield to < from).getOrElse(false)
    )
    MatchAnalysis(
      matchId = batch.matchId,
      gameId = batch.gameId,
      status = batch.status,
      complete = batch.status == "COMPLETE",
      players = players,
      stats = RacingMatchStats(classification, fastestLap, overtakes)
    )

final class ConnectFourAnalyzer extends GameAnalyzer:
  import AnalyzerSupport.*

  def analyze(batch: Batch): MatchAnalysis =
    val moves = batch.events.filter(_.eventType == "MOVE")
    val decisions = batch.events.filter(_.eventType == "AI_DECISION")
    val players = (
      batch.events.filter(_.eventType == "MATCH_STARTED").flatMap(event => strings(event.data, "players")) ++
        batch.events.flatMap(_.playerId)
    ).distinct.sorted.map { playerId =>
      val playerDecisions = decisions.filter(_.playerId.contains(playerId))
      val decisionTimes = playerDecisions.flatMap(event => long(event.data, "timeMs"))
      ConnectFourPlayerStats(
        playerId = playerId,
        moves = moves.count(_.playerId.contains(playerId)),
        aiDecisions = playerDecisions.size,
        totalNodes = playerDecisions.flatMap(event => long(event.data, "nodes")).sum,
        avgAiTimeMs = decisionTimes match
          case Nil => None
          case times => Some(times.sum.toDouble / times.size)
      )
    }
    val finished = batch.events.find(_.eventType == "MATCH_FINISHED")
    val finishData = finished.map(_.data).getOrElse(Obj())
    val complete = batch.status == "COMPLETE"
    val decisionTimes = decisions.flatMap(event => long(event.data, "timeMs"))
    MatchAnalysis(
      matchId = batch.matchId,
      gameId = batch.gameId,
      status = batch.status,
      complete = complete,
      players = players,
      stats = ConnectFourMatchStats(
        durationMs = durationMs(batch),
        moves = int(finishData, "moves").getOrElse(moves.size),
        result = if complete then string(finishData, "result") else None,
        winnerId = if complete then string(finishData, "winnerId") else None,
        aiDecisions = decisions.size,
        totalNodes = decisions.flatMap(event => long(event.data, "nodes")).sum,
        avgAiTimeMs = decisionTimes match
          case Nil => None
          case times => Some(times.sum.toDouble / times.size)
      )
    )

final class SnakeArenaAnalyzer extends GameAnalyzer:
  import AnalyzerSupport.*

  def analyze(batch: Batch): MatchAnalysis =
    val foodEvents = batch.events.filter(_.eventType == "FOOD_EATEN")
    val deaths = batch.events.filter(_.eventType == "PLAYER_DIED")
    val players = (
      batch.events.filter(_.eventType == "MATCH_STARTED").flatMap(event => strings(event.data, "players")) ++
        batch.events.flatMap(_.playerId)
    ).distinct.sorted.map { playerId =>
      val playerFood = foodEvents.filter(_.playerId.contains(playerId))
      val playerDeaths = deaths.filter(_.playerId.contains(playerId))
      SnakePlayerStats(
        playerId = playerId,
        score = playerFood.flatMap(event => int(event.data, "score")).lastOption.getOrElse(0),
        length = playerFood.flatMap(event => int(event.data, "length")).lastOption.getOrElse(0),
        foodEaten = playerFood.size,
        deathCause = playerDeaths.lastOption.flatMap(event => string(event.data, "cause"))
      )
    }
    val finished = batch.events.find(_.eventType == "MATCH_FINISHED")
    val finishData = finished.map(_.data).getOrElse(Obj())
    val complete = batch.status == "COMPLETE"
    val fallbackScore = foodEvents.flatMap(event => int(event.data, "score")).lastOption.getOrElse(0)
    val fallbackLength = foodEvents.flatMap(event => int(event.data, "length")).lastOption.getOrElse(0)
    MatchAnalysis(
      matchId = batch.matchId,
      gameId = batch.gameId,
      status = batch.status,
      complete = complete,
      players = players,
      stats = SnakeMatchStats(
        durationMs = durationMs(batch),
        result = if complete then string(finishData, "result") else None,
        score = int(finishData, "score").getOrElse(fallbackScore),
        length = int(finishData, "length").getOrElse(fallbackLength),
        foodEaten = foodEvents.size,
        deathCause = deaths.lastOption.flatMap(event => string(event.data, "cause"))
      )
    )

final class MemoryMatchAnalyzer extends GameAnalyzer:
  import AnalyzerSupport.*

  def analyze(batch: Batch): MatchAnalysis =
    val flips = batch.events.filter(_.eventType == "CARD_FLIPPED")
    val matches = batch.events.filter(_.eventType == "PAIR_MATCHED")
    val misses = batch.events.filter(_.eventType == "PAIR_MISSED")
    val playerIds = (
      batch.events.filter(_.eventType == "MATCH_STARTED").flatMap(event => strings(event.data, "players")) ++
        batch.events.flatMap(_.playerId)
    ).distinct.sorted
    val attempts = (matches ++ misses).flatMap(event => int(event.data, "attempts")).maxOption
      .getOrElse(matches.size + misses.size)
    val pairs = matches.size
    val accuracy = if attempts == 0 then 0.0 else pairs.toDouble / attempts
    val players = playerIds.map { playerId =>
      val playerMatches = matches.filter(_.playerId.contains(playerId))
      val playerMisses = misses.filter(_.playerId.contains(playerId))
      val playerAttempts = (playerMatches ++ playerMisses).flatMap(event => int(event.data, "attempts")).maxOption
        .getOrElse(playerMatches.size + playerMisses.size)
      val playerPairs = playerMatches.size
      MemoryPlayerStats(
        playerId = playerId,
        pairs = playerPairs,
        attempts = playerAttempts,
        flips = flips.count(_.playerId.contains(playerId)),
        accuracy = if playerAttempts == 0 then 0.0 else playerPairs.toDouble / playerAttempts
      )
    }
    val finished = batch.events.find(_.eventType == "MATCH_FINISHED")
    val finishData = finished.map(_.data).getOrElse(Obj())
    val complete = batch.status == "COMPLETE"
    MatchAnalysis(
      matchId = batch.matchId,
      gameId = batch.gameId,
      status = batch.status,
      complete = complete,
      players = players,
      stats = MemoryMatchStats(
        durationMs = durationMs(batch),
        result = if complete then string(finishData, "result") else None,
        pairs = int(finishData, "pairs").getOrElse(pairs),
        attempts = int(finishData, "attempts").getOrElse(attempts),
        flips = flips.size,
        accuracy = accuracy
      )
    )

object GameAnalyzers:
  val registry: Map[String, GameAnalyzer] = Map(
    "combat-arena" -> new CombatArenaAnalyzer,
    "racing-circuit" -> new RacingCircuitAnalyzer,
    "connect-four" -> new ConnectFourAnalyzer,
    "snake-arena" -> new SnakeArenaAnalyzer,
    "memory-match" -> new MemoryMatchAnalyzer
  )