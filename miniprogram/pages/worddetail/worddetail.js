const store = require('../../utils/store')
const fsrsEngine = require('../../utils/fsrs-engine')

Page({
  data: {
    entry: null,
    parsedResult: null,
    hasContent: false,
    card: null,
    stateLabel: '',
    nextDue: '',
    interval: ''
  },

  onLoad(options) {
    const id = options.id
    if (!id) return

    const entry = store.getWord(id)
    if (!entry) {
      wx.showToast({ title: '单词不存在', icon: 'none' })
      return
    }

    wx.setNavigationBarTitle({ title: entry.word })

    const parsedResult = store.parseResult(entry)
    const hasContent = !!(parsedResult && (
      parsedResult.translation || parsedResult.definition ||
      (parsedResult.definitions && parsedResult.definitions.length > 0) ||
      parsedResult.memory_tips || parsedResult.synonyms ||
      parsedResult.antonyms || parsedResult.etymology ||
      parsedResult.exchange || parsedResult.pos || parsedResult.tag
    ))

    let card = store.getReview(id)
    let stateLabel = 'New'
    let nextDue = ''
    let interval = ''
    if (card) {
      stateLabel = fsrsEngine.STATE_LABELS[card.state] || 'Unknown'
      if (card.due) {
        const dueDate = new Date(card.due)
        const now = new Date()
        const diffMs = dueDate - now
        if (diffMs <= 0) {
          nextDue = 'Due now'
        } else {
          const diffDays = Math.ceil(diffMs / (1000 * 60 * 60 * 24))
          nextDue = diffDays === 1 ? 'Due tomorrow' : 'Due in ' + diffDays + ' days'
        }
      }
      if (card.scheduled_days > 0) {
        interval = fsrsEngine.formatInterval(card.scheduled_days)
      }
    }

    this.setData({
      entry,
      parsedResult,
      hasContent,
      card,
      stateLabel,
      nextDue,
      interval
    })
  }
})
