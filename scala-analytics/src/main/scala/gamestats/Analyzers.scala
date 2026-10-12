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

object GameAnalyzers:
  val registry: Map[String, GameAnalyzer] = Map(
    "combat-arena" -> new CombatArenaAnalyzer,
    "racing-circuit" -> new RacingCircuitAnalyzer
  )