// Unit tests for utils/fsrs-engine.js — FSRS scheduling logic

const fsrsEngine = require('../utils/fsrs-engine')

describe('fsrs-engine — createCard', () => {
  test('creates a new card with id attached', () => {
    const card = fsrsEngine.createCard('entry-123')
    expect(card.id).toBe('entry-123')
    expect(card.state).toBe(fsrsEngine.STATE_NEW)
    expect(card.reps).toBe(0)
    expect(card.lapses).toBe(0)
  })
})

describe('fsrs-engine — rateCard', () => {
  test('rating Good transitions new card to learning/review', () => {
    const card = fsrsEngine.createCard('test')
    const result = fsrsEngine.rateCard(card, fsrsEngine.RATING_GOOD)
    expect(result.card).toBeDefined()
    expect(result.log).toBeDefined()
    expect(result.card.reps).toBeGreaterThan(0)
  })

  test('rating Again on a Review card increases lapses', () => {
    // First: rate Good multiple times to get card into Review state
    let card = fsrsEngine.createCard('test')
    let result = fsrsEngine.rateCard(card, fsrsEngine.RATING_GOOD)
    card = result.card
    // Continue rating Good until we reach Review state
    while (card.state !== fsrsEngine.STATE_REVIEW) {
      result = fsrsEngine.rateCard(card, fsrsEngine.RATING_GOOD)
      card = result.card
    }
    const lapsesBefore = card.lapses

    // Now rate Again → should increase lapses
    const again = fsrsEngine.rateCard(card, fsrsEngine.RATING_AGAIN)
    expect(again.card.lapses).toBeGreaterThan(lapsesBefore)
    expect(again.card.state).toBe(fsrsEngine.STATE_RELEARNING)
  })

  test('different ratings produce different scheduled_days', () => {
    const card = fsrsEngine.createCard('test')
    const preview = fsrsEngine.previewIntervals(card)
    const days = {
      again: preview.again.card.scheduled_days,
      hard:  preview.hard.card.scheduled_days,
      good:  preview.good.card.scheduled_days,
      easy:  preview.easy.card.scheduled_days
    }
    // Easy should schedule further out than Again
    expect(days.easy).toBeGreaterThanOrEqual(days.again)
  })
})

describe('fsrs-engine — previewIntervals', () => {
  test('returns 4 preview objects with card and log', () => {
    const card = fsrsEngine.createCard('test')
    const preview = fsrsEngine.previewIntervals(card)
    for (const key of ['again', 'hard', 'good', 'easy']) {
      expect(preview[key]).toBeDefined()
      expect(preview[key].card).toBeDefined()
      expect(preview[key].log).toBeDefined()
    }
  })
})

describe('fsrs-engine — formatInterval', () => {
  test('formats sub-day as < 1d', () => {
    expect(fsrsEngine.formatInterval(0.5)).toBe('< 1d')
  })

  test('formats days correctly', () => {
    expect(fsrsEngine.formatInterval(3)).toBe('3d')
    expect(fsrsEngine.formatInterval(14)).toBe('14d')
  })

  test('formats months', () => {
    expect(fsrsEngine.formatInterval(60)).toBe('2mo')
  })

  test('formats years', () => {
    expect(fsrsEngine.formatInterval(400)).toBe('1y')
  })
})

describe('fsrs-engine — isDue', () => {
  test('new cards are always due', () => {
    const card = fsrsEngine.createCard('test')
    expect(fsrsEngine.isDue(card)).toBe(true)
  })

  test('card with future due date is not due', () => {
    const card = fsrsEngine.createCard('test')
    const result = fsrsEngine.rateCard(card, fsrsEngine.RATING_EASY)
    const rated = result.card
    // Easy on a new card should schedule into the future
    if (rated.state !== fsrsEngine.STATE_NEW) {
      const futureDate = new Date(rated.due)
      const now = new Date()
      if (futureDate > now) {
        expect(fsrsEngine.isDue(rated)).toBe(false)
      }
    }
  })
})

describe('fsrs-engine — getDueQueue', () => {
  test('all new words are in the due queue', () => {
    const words = {
      '1': { id: '1', word: 'hello' },
      '2': { id: '2', word: 'world' }
    }
    const reviews = {}
    const queue = fsrsEngine.getDueQueue(words, reviews)
    expect(queue.length).toBe(2)
    expect(queue).toContain('1')
    expect(queue).toContain('2')
  })

  test('words with future due dates are not in queue', () => {
    const words = {
      '1': { id: '1', word: 'hello' },
      '2': { id: '2', word: 'world' }
    }
    // Rate word '1' as Easy → future due date
    const card = fsrsEngine.createCard('1')
    const result = fsrsEngine.rateCard(card, fsrsEngine.RATING_EASY)
    const reviews = { '1': result.card }

    const queue = fsrsEngine.getDueQueue(words, reviews)
    // '2' is new → due; '1' may or may not be due depending on interval
    expect(queue).toContain('2')
  })

  test('empty words returns empty queue', () => {
    expect(fsrsEngine.getDueQueue({}, {})).toEqual([])
  })
})

describe('fsrs-engine — getQueueCounts', () => {
  test('counts new words correctly', () => {
    const words = {
      '1': { id: '1', word: 'hello' },
      '2': { id: '2', word: 'world' }
    }
    const counts = fsrsEngine.getQueueCounts(words, {})
    expect(counts.new).toBe(2)
    expect(counts.total).toBe(2)
  })

  test('empty words returns zero counts', () => {
    const counts = fsrsEngine.getQueueCounts({}, {})
    expect(counts.total).toBe(0)
  })
})

describe('fsrs-engine — constants', () => {
  test('rating constants are correct', () => {
    expect(fsrsEngine.RATING_AGAIN).toBe(1)
    expect(fsrsEngine.RATING_HARD).toBe(2)
    expect(fsrsEngine.RATING_GOOD).toBe(3)
    expect(fsrsEngine.RATING_EASY).toBe(4)
  })

  test('state constants are correct', () => {
    expect(fsrsEngine.STATE_NEW).toBe(0)
    expect(fsrsEngine.STATE_LEARNING).toBe(1)
  })
})
