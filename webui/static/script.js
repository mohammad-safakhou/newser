document.addEventListener('DOMContentLoaded', () => {
  const form = document.getElementById('create-topic-form');
  const titleInput = document.getElementById('topic-title');
  const tableBody = document.querySelector('#topics-table tbody');
  const createBtn = document.getElementById('create-button');

  async function loadTopics() {
    tableBody.innerHTML = '';
    try {
      const res = await fetch('/topics');
      const topics = await res.json();
      topics.forEach(topic => {
        const tr = document.createElement('tr');
        // Title
        const tdTitle = document.createElement('td');
        const a = document.createElement('a');
        a.href = `/topic.html?title=${encodeURIComponent(topic.title)}`;
        a.textContent = topic.title;
        tdTitle.appendChild(a);
        tr.appendChild(tdTitle);
        // State
        const tdState = document.createElement('td');
        tdState.textContent = topic.state;
        tr.appendChild(tdState);
        // CreatedAt
        const tdCreated = document.createElement('td');
        tdCreated.textContent = new Date(topic.created_at).toLocaleString();
        tr.appendChild(tdCreated);
        // UpdatedAt
        const tdUpdated = document.createElement('td');
        tdUpdated.textContent = topic.updated_at ? new Date(topic.updated_at).toLocaleString() : '-';
        tr.appendChild(tdUpdated);
        // Actions
        const tdActions = document.createElement('td');
        const viewBtn = document.createElement('button');
        viewBtn.textContent = 'View';
        viewBtn.addEventListener('click', () => {
          window.location.href = `/topic.html?title=${encodeURIComponent(topic.title)}`;
        });
        tdActions.appendChild(viewBtn);
        tr.appendChild(tdActions);
        tableBody.appendChild(tr);
      });
    } catch (err) {
      console.error('Error loading topics', err);
    }
  }

  form.addEventListener('submit', async e => {
    e.preventDefault();
    const title = titleInput.value.trim();
    if (!title) return;

    createBtn.disabled = true;
    createBtn.textContent = 'Creating...';
    try {
      await fetch('/topics', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({title})
      });
      titleInput.value = '';
      await loadTopics();
    } catch (err) {
      console.error('Error creating topic', err);
    } finally {
      createBtn.disabled = false;
      createBtn.textContent = 'Create Topic';
    }
  });

  loadTopics();
});
