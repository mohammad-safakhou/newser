document.addEventListener('DOMContentLoaded', async () => {
  const params = new URLSearchParams(window.location.search);
  const title = params.get('title');
  if (!title) return;

  const backBtn = document.getElementById('back-button');
  const detailsBody = document.querySelector('#details-table tbody');
  const historyDiv = document.getElementById('history');
  const messageInput = document.getElementById('message');
  const form = document.getElementById('modify-topic-form');
  const sendBtn = document.getElementById('send-button');
  const newsBtn = document.getElementById('generate-news');
  const newsOutput = document.getElementById('news-output');
  const topicTitleEl = document.getElementById('topic-title');

  topicTitleEl.textContent = title;

  backBtn.addEventListener('click', () => {
    window.location.href = '/';
  });

  async function loadTopic() {
    // Clear previous details and history
    detailsBody.innerHTML = '';
    historyDiv.innerHTML = '';
    try {
      const res = await fetch(`/topics/${encodeURIComponent(title)}`);
      const topic = await res.json();

      // Populate details table
      const fields = [
        ['Title', topic.title],
        ['State', topic.state],
        ['Created At', new Date(topic.created_at).toLocaleString()],
        ['Updated At', topic.updated_at ? new Date(topic.updated_at).toLocaleString() : '-'],
        ['Subtopics', (topic.subtopics || []).join(', ') || '-'],
        ['Key Concepts', (topic.key_concepts || []).join(', ') || '-'],
        ['Related Topics', (topic.related_topics || []).join(', ') || '-'],
        ['Cron Spec', topic.cron_spec || '-'],
        ['Preferences', Object.entries(topic.preferences || {}).map(([k, v]) => `${k}: ${v}`).join(', ') || '-']
      ];
      fields.forEach(([key, value]) => {
        const tr = document.createElement('tr');
        const th = document.createElement('th'); th.textContent = key;
        const td = document.createElement('td'); td.textContent = value;
        tr.appendChild(th); tr.appendChild(td);
        detailsBody.appendChild(tr);
      });

      // Populate history
      topic.history.forEach(item => {
        const parts = item.split('\nLLM: ');
        const userPart = parts[0].replace(/^User: /, '');
        const llmPart = parts[1] || '';

        const userDiv = document.createElement('div');
        userDiv.classList.add('message', 'user');
        userDiv.textContent = userPart;
        historyDiv.appendChild(userDiv);

        const llmDiv = document.createElement('div');
        llmDiv.classList.add('message', 'llm');
        llmDiv.textContent = llmPart;
        historyDiv.appendChild(llmDiv);
      });
      historyDiv.scrollTop = historyDiv.scrollHeight;
    } catch (err) {
      console.error('Error loading topic', err);
    }
  }

  // Handle user message send
  form.addEventListener('submit', async e => {
    e.preventDefault();
    const msg = messageInput.value.trim();
    if (!msg) return;
    sendBtn.disabled = true;
    sendBtn.textContent = 'Sending...';
    try {
      await fetch(`/topics/${encodeURIComponent(title)}`, {
        method: 'PUT',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({message: msg})
      });
      messageInput.value = '';
      await loadTopic();
    } catch (err) {
      console.error('Error modifying topic', err);
    } finally {
      sendBtn.disabled = false;
      sendBtn.textContent = 'Send';
    }
  });

  // Handle news generation
  newsBtn.addEventListener('click', async () => {
    newsBtn.disabled = true;
    newsBtn.textContent = 'Generating...';
    newsOutput.innerHTML = '';
    try {
      const res = await fetch(`/topics/${encodeURIComponent(title)}/generate-news`);
      const data = await res.json();
      let text = data.news || '';
      text = text.replace(/^```[a-z]*\n/, '').replace(/```$/, '');
      newsOutput.innerHTML = marked.parse(text);
    } catch (err) {
      console.error('Error generating news', err);
      newsOutput.textContent = 'Error generating news.';
    } finally {
      newsBtn.disabled = false;
      newsBtn.textContent = 'Generate News';
    }
  });

  // Initial load
  await loadTopic();
});
